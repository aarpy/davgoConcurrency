package main

import (
   "bytes"
   "encoding/json"
   "errors"
   "flag"
   "fmt"
   "io"
   "io/ioutil"
   "log"
   "math/rand"
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
   Disconnect chan *Peer
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
      return nil, err
   }
   return &m, nil
}

func (p *Peer) Close() {
   close(p.TxCh)
   p.conn.Close()
}

func chatPeer(conn net.Conn, rxCh chan *PeerMessage, errCh chan *PeerError, disconnectCh chan *Peer, stop chan struct{}) *Peer {
   txCh := make(chan *Message)
   peer := &Peer{txCh, rxCh, errCh, disconnectCh, conn, newJSONEncoder(conn), newJSONDecoder(conn)}
   readStopped := make(chan struct{})

   // go routine to read client's messages
   go func() {
      for {
         msg, err := peer.Read()
         if err != nil {
            close(readStopped)
            if err == io.EOF {
               disconnectCh <- peer
            } else {
               errCh <- &PeerError{peer, err}
            }
            return
         }
         rxCh <- &PeerMessage{peer, msg}
      }
   }()

   // go routine to write messages to client
   go func() {
      Loop:
      for {
         select {
         case <-readStopped:
            break Loop
         case <-stop:
            conn.Close() // to free up the read go routine
            disconnectCh <- peer
            break Loop
         case m := <-txCh:
            if err := peer.Write(m); err != nil {
               errCh <- &PeerError{peer, err}
               break Loop
            }
         }
      }
   }()

   return peer
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
   if rmPeer(err.Peer) {
      err.Peer.Close()
   }
}

func handleDisconnect(p *Peer) {
   if rmPeer(p) {
      p.Close()
      fmt.Printf("peer %s disconnected\n", p.RemoteAddress())
   }
}

func sendAll(msg *Message, me *Peer) {
   for _, peer := range peers {
      if peer != me {
         peer.TxCh <- msg
      }
   }
}

func rmPeer(peer *Peer) bool {
   for i, p := range peers {
      if peer == p {
         peers = append(peers[:i], peers[i + 1:]...)
         return true
      }
   }
   return false
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

   // peers notify main when they disconnect on this channel
   disconnectCh := make(chan *Peer)

   // connect to another server?
   var me *Peer
   if addr != "" {
      conn, err := net.Dial("tcp", addr)
      fatalIfErr(err)
      me := chatPeer(conn, rxCh, errCh, disconnectCh, stop)
      peers = append(peers, me)
   }

   // run in chat bot mode?
   if bot {
      loadQuotes()
      go chatBot(user, txCh, stop)
   }

   // main loop
   for {
      select {
      case err := <-errCh:
         handleError(err)
      case peer := <-disconnectCh:
         handleDisconnect(peer)
      case sr := <-srvCh:
         if sr.Err != nil {
            fmt.Println(sr.Err)
            continue
         }
         peer := chatPeer(sr.Conn, rxCh, errCh, disconnectCh, stop)
         peers = append(peers, peer)
         fmt.Printf("peer joined from %s\n", peer.RemoteAddress())
      case msg := <-rxCh:
         handleMessage(msg)
      case msg := <-txCh:
         sendAll(msg, me)
      }
   }
}

func chatBot(user string, txCh chan *Message, stop chan struct{}) {
   for fails := 0; fails < 5; {
      select {
      case <-stop:
         fmt.Println("chat bot exiting")
         return
      case <-time.After(2000 * time.Millisecond):
         if len(peers) == 0 {
            continue
         }
         quote, err := GetCachedQuote()
         if err != nil {
            fails++
            continue
         } else {
            fails = 0
         }
         txCh <- &Message{user, quote.String()}
      }
   }
   fmt.Println("Chat bot died. :-(  R.I.P.")
}

type Quotes struct {
   QuoteOfTheDay string
   Author string
}

func (q *Quotes) String() string {
   return fmt.Sprintf(`"%s" --%s`, q.QuoteOfTheDay, q.Author)
}

var quotes []*Quotes

// Loads file into quote cache.
func loadQuotes() {
   b, err := ioutil.ReadFile("quotes.json")
   if err != nil {
      log.Fatalln(err)
   }
   r := bytes.NewReader(b)
   dec := json.NewDecoder(r)
   for {
      var quote Quotes
      if err = dec.Decode(&quote); err == io.EOF {
         break
      } else if err != nil {
         log.Fatalln(err)
      }
      quotes = append(quotes, &quote)
   }
}

// Get a quote from local cache.
func GetCachedQuote() (*Quotes, error) {
   return quotes[rand.Intn(len(quotes))], nil
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

