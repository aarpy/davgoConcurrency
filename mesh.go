package main

import (
   "errors"
   "fmt"
   "log"
   "net"
   "strings"
   "time"
)

type ServeResult struct {
   Conn net.Conn
   Err error
}

func Serve(addr string, stop chan struct{}) (chan *ServeResult, error) {
   ln, err := net.Listen("tcp", addr)
   if err != nil {
      return nil, err
   }

   go func() { <-stop; err := ln.Close(); panicIfErr(err); }()

   ch := make(chan *ServeResult)

   go func() {
      defer close(ch)
      for {
         conn, err := ln.Accept()
         switch {
         case err == nil:
            ch <- &ServeResult { conn, nil }
         case strings.Contains(err.Error(), "use of closed network connection"):
            ch <- &ServeResult { nil, errors.New("normal server shutdown") }
            return
         default:
            ch <- &ServeResult { nil, err }
            return
         }
      }
   }()

   return ch, nil
}

// Message is the data that travels between peers.
type Message struct {
   Text string
}

// PeerMessage is the result of reading from a peer connection.
type PeerMessage struct {
   Peer *Peer
   Msg Message
}

type PeerError struct {
   Peer *Peer
   Err error
}

type Peer struct {
   Conn net.Conn
   TxCh chan *Message
   RxCh chan *PeerMessage
   ErrCh chan *PeerError
}

func ChatPeer(conn net.Conn, rxCh chan *PeerMessage, errCh chan *PeerError, stop chan struct{}) (*Peer, error) {
   txCh := make(chan *Message)
   peer := &Peer{conn, txCh, rxCh, errCh}
   ioerr := make(chan struct{})

   // go routine to read client's messages
   go func() {
      var buf [1024]byte
      for {
         n, err := conn.Read(buf[:])
         if err != nil {
            close(ioerr)
            errCh <- &PeerError{peer, err}
            return
         }
         rxCh <- &PeerMessage{peer, Message{string(buf[:n])}}
      }
   }()

   // go routine to write messages to client
   go func() {
      defer close(txCh)
      for {
         select {
         case <-stop:
            // this should free up the pending read in the other go routine
            conn.Close()
            return
         case m := <-txCh:
            conn.Write([]byte(m.Text))
         }
      }
   }()

   return &Peer{conn, txCh, rxCh, errCh}, nil
}

func chatBot(txCh chan *Message, stop chan struct{}) {
   for i := 0;; i++ {
      select {
      case <-time.After(time.Second):
         txCh <- &Message{"hello" + string(i)}
      case <-stop:
         fmt.Println("chat bot shutdown normally")
         return
      }
   }
}

func main() {
   stop := make(chan struct{})
   serve, err := Serve(":4343", stop)
   fatalIfErr(err)

   var peers []*Peer

   for {
      select {
      case sr := <-serve:
         if sr.Err != nil {
            fmt.Println(sr.Err)
            continue
         }
         peer, err := ChatPeer(sr.Conn)
         if err != nil {
            fmt.Println(err)
            continue
         }
         peers = append(peers, peer)
         chatBot(peer.TxCh)
      }
   }
}

func fatalIfErr(err error) {
   if err != nil {
      log.Fatalln(err)
   }
}

func panicIfErr(err error) {
   if err != nil {
      panic(err)
   }
}
