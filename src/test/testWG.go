package main

import (
	"log"
	"sync"
	"time"
)

var wg1 sync.WaitGroup

func main() {

	wg1.Add(1)

	go reduceWG()

	wg1.Wait()

	log.Println("Done")

}

func reduceWG() {
	defer wg1.Done()
	time.Sleep(time.Second * 1)
}
