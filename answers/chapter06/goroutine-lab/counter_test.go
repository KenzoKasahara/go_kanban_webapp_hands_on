//go:build !racedemo

package lab

import (
	"sync"
	"testing"
)

// Step 4 の完成形。Step 3 の Data Race 版は counter_race_test.go にある。
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
