package main

import (
	"fmt"
	"time"
)

func work(name string) {
	for i := 1; i <= 3; i++ {
		fmt.Println(name, i)
		time.Sleep(100 * time.Millisecond)
	}
}

func main() {
	go work("A") // goroutine として開始し、すぐ次の行へ進む
	work("B")    // main の goroutine で実行する
	time.Sleep(500 * time.Millisecond)
}
