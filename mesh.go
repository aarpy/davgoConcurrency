package main

import (
   "encoding/json"
   "errors"
   "flag"
   "fmt"
   "io"
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

type Encoder interface {
   Encode(v interface{}) error
}

type Decoder interface {
   Decode(v interface{}) error
}

// Message is the data that travels between peers.
type Message struct {
   User string
   Text string
}

func newJSONEncoder(w io.Writer) Encoder {
   enc := json.NewEncoder(w)
   return enc
}

func newJSONDecoder(r io.Reader) Decoder {
   dec := json.NewDecoder(r)
   return dec
}

// PeerMessage is the result of reading from a peer connection.
type PeerMessage struct {
   Peer *Peer
   Msg *Message
}

type PeerError struct {
   Peer *Peer
   Err error
}

type Peer struct {
   TxCh chan *Message
   RxCh chan *PeerMessage
   ErrCh chan *PeerError
   conn net.Conn
   enc Encoder
   dec Decoder
}

func (p *Peer) RemoteAddress() string {
   conn := p.conn.(*net.TCPConn)
   return conn.RemoteAddr().String()
}

func (p *Peer) Write(m *Message) (error) {
   return p.enc.Encode(m)
}

func (p *Peer) Read() (*Message, error) {
   var m Message
   if err := p.dec.Decode(&m); err != nil {
      fmt.Println("here")
      return nil, err
   }
   return &m, nil
}

func ChatPeer(conn net.Conn, rxCh chan *PeerMessage, errCh chan *PeerError, stop chan struct{}) *Peer {
   txCh := make(chan *Message)
   peer := &Peer{txCh, rxCh, errCh, conn, newJSONEncoder(conn), newJSONDecoder(conn)}
   ioerr := make(chan struct{})

   // go routine to read client's messages
   go func() {
      for {
         msg, err := peer.Read()
         if err != nil {
            close(ioerr)
            errCh <- &PeerError{peer, err}
            return
         }
         rxCh <- &PeerMessage{peer, msg}
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
            if err := peer.Write(m); err != nil {
               close(ioerr)
               return
            }
         }
      }
   }()

   return peer
}

func chatBot(user string, txCh chan *Message, stop chan struct{}) {
   for i := 0;; i++ {
      select {
      case <-time.After(time.Second):
         txt := fmt.Sprintf("hello%d", i)
         txCh <- &Message{user, txt}
      case <-stop:
         fmt.Println("chat bot shutdown normally")
         return
      }
   }
}

func handleMessage(msg *PeerMessage) {
   fmt.Printf("%s: %s\n", msg.Msg.User, msg.Msg.Text)
   for _, peer := range peers {
      if peer != msg.Peer {
         peer.TxCh <- msg.Msg
      }
   }
}

func handleError(err *PeerError) {
   fmt.Println(err.Err)
   rmPeer(err.Peer)
}

func sendAll(msg *Message, me *Peer) {
   for _, peer := range peers {
      if peer != me {
         peer.TxCh <- msg
      }
   }
}

func rmPeer(peer *Peer) {
   for i, p := range peers {
      if peer == p {
         peers = append(peers[:i], peers[i + 1:]...)
         return
      }
   }
}

// global array of connected peers
var peers []*Peer

func main() {
   var user string
   flag.StringVar(&user, "u", "anonymous", "chat user name")
   var laddr string
   flag.StringVar(&laddr, "l", ":4242", "address:port to listen on")
   var addr string
   flag.StringVar(&addr, "a", "", "address:port to connect to")
   var bot bool
   flag.BoolVar(&bot, "b", false, "enables chat bot")

   flag.Parse()

   // closing the stop channel signals all go routines to exit
   stop := make(chan struct{})

   // server sends newly connected peers back to main on this channel
   srvCh, err := Serve(laddr, stop)
   fatalIfErr(err)
   fmt.Printf("listening on %s...\n", laddr)

   // peers send messages back to main on this channel
   rxCh := make(chan *PeerMessage)

   // peers send errors back to main on this channel
   errCh := make(chan *PeerError)

   // local transmit channel...used by chatBot and the UI
   txCh := make(chan *Message)

   // connect to another server?
   var me *Peer
   if addr != "" {
      conn, err := net.Dial("tcp", addr)
      fatalIfErr(err)
      me := ChatPeer(conn, rxCh, errCh, stop)
      peers = append(peers, me)
   }

   // run in chat bot mode?
   if bot {
      go chatBot(user, txCh, stop)
   }

   // main loop
   for {
      select {
      case sr := <-srvCh:
         if sr.Err != nil {
            fmt.Println(sr.Err)
            continue
         }
         peer := ChatPeer(sr.Conn, rxCh, errCh, stop)
         peers = append(peers, peer)
         fmt.Printf("peer joined from %s\n", peer.RemoteAddress())
      case msg := <-rxCh:
         handleMessage(msg)
      case err := <-errCh:
         handleError(err)
      case msg := <-txCh:
         sendAll(msg, me)
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
