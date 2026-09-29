package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	wg.Add(1) // 未完了の数を +1

	go func() {
		defer wg.Done() // 終わったら -1
		fmt.Println("background work")
	}()

	wg.Wait() // 未完了が 0 になるまで待つ
	fmt.Println("main: done")
}
