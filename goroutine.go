package main

import (
   "fmt"
   "time"
)

func slowProcess() {
   fmt.Println("slow process started...")
   <-time.After(3 * time.Second)
   fmt.Println("slow process finished.")
}

func main() {
   slowProcess()
   fmt.Println("Printed after slowProcess() call.")

   // other slow work in main "thread"
   <-time.After(3200 * time.Millisecond)
}
