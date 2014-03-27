package main

import (
   "fmt"
)

func main() {
   ch := make(chan int)

   ch <- 42
   i := <-ch
   fmt.Println(i)
}
