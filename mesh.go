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

// message is the data that travels between peers.
type message struct {
	User string
	Text string
}

// peerMessage is a message recieved from a peer.
type peerMessage struct {
	Peer *peer
	Msg  *message
}

// peerError is a error related to communication with a peer.
type peerError struct {
	Peer *peer
	Err  error
}

// peer contains the transmit, receive, error, and disconnect
// channels for communicating with a peer.
type peer struct {
	TxCh       chan *message
	RxCh       chan *peerMessage
	ErrCh      chan *peerError
	Disconnect chan *peer

    // private members
	conn       net.Conn
	enc        encoder
	dec        decoder
}

func (p *peer) RemoteAddress() string {
	conn := p.conn.(*net.TCPConn)
	return conn.RemoteAddr().String()
}

func (p *peer) Write(m *message) error {
	return p.enc.Encode(m)
}

func (p *peer) Read() (*message, error) {
	var m message
	if err := p.dec.Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (p *peer) Close() {
	close(p.TxCh)
	p.conn.Close()
}

// encoder / decoder interfaces
type encoder interface {
	Encode(v interface{}) error
}

type decoder interface {
	Decode(v interface{}) error
}

// newJSONEncoder returns a JSON encoder that implements
// the encoder interface.
func newJSONEncoder(w io.Writer) encoder {
	enc := json.NewEncoder(w)
	return enc
}

// newJSONDecoder returns a JSON decoder whtat implements
// the decoder interface.
func newJSONDecoder(r io.Reader) decoder {
	dec := json.NewDecoder(r)
	return dec
}

// serveResult contains either a new peer connection or an error.
type serveResult struct {
	Conn net.Conn
	Err  error
}

// serve starts a listening socket and returns a channel on which
// new connections and errors will be pased.
func serve(addr string, stop chan struct{}) (chan *serveResult, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	go func() { <-stop; err := ln.Close(); panicIfErr(err) }()

	ch := make(chan *serveResult)

	go func() {
		defer close(ch)
		for {
			conn, err := ln.Accept()
			switch {
			case err == nil:
				ch <- &serveResult{conn, nil}
			case strings.Contains(err.Error(), "use of closed network connection"):
				ch <- &serveResult{nil, errors.New("normal server shutdown")}
				return
			default:
				ch <- &serveResult{nil, err}
				return
			}
		}
	}()

	return ch, nil
}

// chatPeer starts background go routines to communicate with a single peer and
// returns a peer struct.  The caller uses channels in the peer struct to
// communicate with the peer.
func chatPeer(conn net.Conn, rxCh chan *peerMessage, errCh chan *peerError, disconnectCh chan *peer, stop chan struct{}) *peer {
	txCh := make(chan *message)
	peer := &peer{txCh, rxCh, errCh, disconnectCh, conn, newJSONEncoder(conn), newJSONDecoder(conn)}
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
					errCh <- &peerError{peer, err}
				}
				return
			}
			rxCh <- &peerMessage{peer, msg}
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
					errCh <- &peerError{peer, err}
					break Loop
				}
			}
		}
	}()

	return peer
}

// global array of connected peers
var g_peers []*peer

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
	srvCh, err := serve(laddr, stop)
	fatalIfErr(err)
	fmt.Printf("listening on %s...\n", laddr)

	// peers send messages back to main on this channel
	rxCh := make(chan *peerMessage, 1000)

	// peers send errors back to main on this channel
	errCh := make(chan *peerError)

	// local transmit channel...used by chatBot and the UI
	txCh := make(chan *message)

	// peers notify main when they disconnect on this channel
	disconnectCh := make(chan *peer)

	// connect to another server?
	var me *peer
	if addr != "" {
		conn, err := net.Dial("tcp", addr)
		fatalIfErr(err)
		me := chatPeer(conn, rxCh, errCh, disconnectCh, stop)
		g_peers = append(g_peers, me)
	}

	// run in chat bot mode?
	if bot {
        rand.Seed(time.Now().UTC().UnixNano())
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
			g_peers = append(g_peers, peer)
			fmt.Printf("peer joined from %s\n", peer.RemoteAddress())
		case msg := <-rxCh:
			handleMessage(msg)
		case msg := <-txCh:
			sendAll(msg, me)
		}
	}
}

// handleMessage prints a message received from a peer and sends it
// to all other peers.
func handleMessage(msg *peerMessage) {
	fmt.Printf("%s: %s\n", msg.Msg.User, msg.Msg.Text)
	for _, peer := range g_peers {
		if peer != msg.Peer {
			peer.TxCh <- msg.Msg
		}
	}
}

// handleError prints a error from a peer and then removes that
// peer from the global list.
func handleError(err *peerError) {
	fmt.Println(err.Err)
	if rmPeer(err.Peer) {
		err.Peer.Close()
	}
}

// handleDisconnect removes the peer from the global list and
// lets the user know a peer disconnected.
func handleDisconnect(p *peer) {
	if rmPeer(p) {
		p.Close()
		fmt.Printf("peer %s disconnected\n", p.RemoteAddress())
	}
}

// sendAll broadcasts a messages to all peers with the exception
// of "me" (i.e., don't send the message to ourself).
func sendAll(msg *message, me *peer) {
	for _, peer := range g_peers {
		if peer != me {
			peer.TxCh <- msg
		}
	}
}

// rmPeer removes a peer from the global list.
func rmPeer(p *peer) bool {
	for i, p2 := range g_peers {
		if p == p2 {
			g_peers = append(g_peers[:i], g_peers[i+1:]...)
			return true
		}
	}
	return false
}

// chatBot sends random quotes over the supplied channel.
func chatBot(user string, txCh chan *message, stop chan struct{}) {
	for fails := 0; fails < 5; {
		select {
		case <-stop:
			fmt.Println("chat bot exiting")
			return
		case <-time.After(5000 * time.Millisecond):
			if len(g_peers) == 0 {
				continue
			}
			quote, err := GetCachedQuote()
			if err != nil {
				fails++
				continue
			} else {
				fails = 0
			}
			txCh <- &message{user, quote.String()}
		}
	}
	fmt.Println("Chat bot died. :-(  R.I.P.")
}

// quotes represents a response from Mokashi webservice.
type quotes struct {
	QuoteOfTheDay string
	Author        string
}

func (q *quotes) String() string {
	return fmt.Sprintf(`"%s" --%s`, q.QuoteOfTheDay, q.Author)
}

var g_quotes []*quotes

// Loads file into quote cache.
func loadQuotes() {
	b, err := ioutil.ReadFile("quotes.json")
	if err != nil {
		log.Fatalln(err)
	}
	r := bytes.NewReader(b)
	dec := json.NewDecoder(r)
	for {
		var quote quotes
		if err = dec.Decode(&quote); err == io.EOF {
			break
		} else if err != nil {
			log.Fatalln(err)
		}
		g_quotes = append(g_quotes, &quote)
	}
}

// Get a quote from local cache.
func GetCachedQuote() (*quotes, error) {
	return g_quotes[rand.Intn(len(g_quotes))], nil
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
