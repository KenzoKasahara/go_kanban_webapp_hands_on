package lab

import (
	"sync"
	"testing"
)

func TestCounterRace(t *testing.T) {
	counter := 0
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock() // 他の goroutine はここで待つ
			counter++
			mu.Unlock()
		}()
	}

	wg.Wait()
	t.Logf("counter=%d", counter)
}
