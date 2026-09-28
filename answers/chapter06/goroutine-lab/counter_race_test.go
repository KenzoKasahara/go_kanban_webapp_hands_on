//go:build racedemo

package lab

import (
	"sync"
	"testing"
)

// Step 3 の Data Race 版。-tags racedemo を付けたときだけビルドする。
// 既定のビルドに含めると go test -race ./... が必ず失敗するため分けている。
func TestCounterRace(t *testing.T) {
	counter := 0
	var wg sync.WaitGroup

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter++ // 同期なしで共有変数を書き換える
		}()
	}

	wg.Wait()
	t.Logf("counter=%d", counter)
}
