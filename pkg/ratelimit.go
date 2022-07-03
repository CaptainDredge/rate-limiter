// To execute Go code, please declare a func main() in a package "main"

package main

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

func main() {

	ctx := context.Background()
	ctx, _ = context.WithDeadline(ctx, time.Now().Add(time.Duration(2*time.Second)))
	
	limiter := NewLimiter(1, 2)

	err := limiter.WaitN(ctx, 3)

	print(err)

	err = limiter.WaitN(ctx, 2)

	print(err)

	err = limiter.WaitN(ctx, 2)

	print(err)
}

func print(err error){
	if err == nil {
		fmt.Println("Successfull request")
	}else{
		fmt.Println(err.Error())
	}
}

type Limit int

type Limiter struct{
	limit Limit
	burst int
	token int
	last time.Time
	lastEvent time.Time
	mu sync.Mutex
}

type Reservation struct{
	ok bool
	lim *Limiter
	token int
	limit Limit
	timeToAct time.Time
}

func NewLimiter(r int, b int) *Limiter{
	return &Limiter{
		limit: Limit(r),
		burst: b,
	}
}

func (limit Limit) tokenFromDuration(duration time.Duration) int{
	seconds := duration.Seconds()

	return int(seconds)*int(limit)
}

func (limit Limit) durationFromToken(token int) time.Duration {
	seconds := float64(token)/float64(limit)
	return time.Duration(seconds*float64(time.Second))
}

func (limiter *Limiter) WaitN(ctx context.Context, n int) error{

	limiter.mu.Lock()
	burst := limiter.burst
	limiter.mu.Unlock()

	if n >burst{
		return fmt.Errorf("Requests exceed burst")
	}
	
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var waitLimit time.Duration = 0
	now := time.Now()
	if deadline, ok := ctx.Deadline(); ok{
		waitLimit = deadline.Sub(now)
	}

	reservation := limiter.reserveN(n, now, waitLimit)

	delay := reservation.delayFrom(now)

	if delay == 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <- ctx.Done():
		reservation.cancel(now)
		return ctx.Err()
	case <- timer.C:
		return nil
	}
}

func (limiter *Limiter) Wait(ctx context.Context){
	limiter.WaitN(ctx, 1);
}

func (limiter *Limiter) reserveN(n int, now time.Time, waitLimit time.Duration) Reservation {

	limiter.mu.Lock()

	defer limiter.mu.Unlock()

	token, last := limiter.update(now)

	token -= n

	waitDuration := time.Duration(0)
	if token < 0 {
		waitDuration = limiter.limit.durationFromToken(-token)
	}

	ok := n <= limiter.burst && waitDuration < waitLimit

	if ok {
		limiter.token = token
		limiter.last = now
		limiter.lastEvent = now.Add(waitDuration)
	}else {
		limiter.last = last
	}

	return Reservation{
		ok: ok,
		lim: limiter,
		token: n,
		limit: limiter.limit,
		timeToAct: now.Add(waitDuration),
	}
}

func (limiter *Limiter) update(now time.Time) (token int, last time.Time){
	last = limiter.last

	if now.Before(last) {
		last = now
	}

	token = limiter.token + limiter.limit.tokenFromDuration(now.Sub(last))

	if token > limiter.burst {
		token = limiter.burst
	}

	return token, last
}

func (reservation *Reservation) delayFrom(now time.Time) time.Duration{

		if !reservation.ok {
			return time.Duration(math.MaxInt);
		}

		delay := reservation.timeToAct.Sub(now)

		if delay < 0 {
			delay = 0
		}

		return delay
}

func (reservation *Reservation) cancel(now time.Time){
	if !reservation.ok {
		return
	}

	reservation.lim.mu.Lock()

	defer reservation.lim.mu.Unlock()

	tokenToBeReplenished := reservation.token - reservation.lim.limit.tokenFromDuration(reservation.lim.lastEvent.Sub(reservation.timeToAct))

	if tokenToBeReplenished < 0 {
		return
	}

	token, _ := reservation.lim.update(now)

	token += tokenToBeReplenished

	if token > reservation.lim.burst {
		token = reservation.lim.burst
	}
	reservation.lim.token = token
	reservation.lim.last = now
}