package main

import (
   "fmt"
   "time"
)

func talk(id string, msgCh chan string) {
   for i := 0;; i++ {
      msgCh <- fmt.Sprintf("%s - %d", id, i)
      <-time.After(2 * time.Second)
   }
}

func main() {
   msgCh := make(chan string)
   go talk("fred", msgCh)
   go talk("mark", msgCh)

   for {
      msg := <-msgCh
      fmt.Println(msg)
   }
}
