package main

import (
	"bytes"
	"fmt"
	"github.com/dzrry/ws-chat/internal"
	"html/template"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	MaxBufferOutputBytes = 1024
)

func getTemplateFilePath(file string) string {
	return "internal/template/" + file
}

func sendPageData(w http.ResponseWriter, pageDataBytes []byte, contentType string) error {
	w.Header().Set("Content-Type", contentType)

	numBytes := len(pageDataBytes)
	for numBytes > 0 {
		numWrites, err := w.Write(pageDataBytes)
		if err != nil {
			return err
		}
		numBytes -= numWrites
	}

	return nil
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	httpTemplate, err := template.ParseFiles(getTemplateFilePath("home.html"))
	if err != nil {
		sendPageData(w, []byte("Parse template error."), "text/plain; charset=utf-8")
		return
	}

	var buf bytes.Buffer
	if err = httpTemplate.Execute(&buf, nil); err != nil {
		sendPageData(w, []byte("Render page error."), "text/plain; charset=utf-8")
		return
	}

	httpContentCache := buf.Bytes()
	sendPageData(w, httpContentCache, "text/html; charset=utf-8")
}

type ChatConn struct { // implement chat.ReadWriteCloser
	Conn *websocket.Conn

	InputBuffer  bytes.Buffer
	OutputBuffer bytes.Buffer
}

func (cc *ChatConn) ReadFromBuffer(b []byte, from int) int {
	to := len(b) - from
	if to > cc.InputBuffer.Len() {
		to = cc.InputBuffer.Len()
	}

	to += from
	for from < to {
		b[from], _ = cc.InputBuffer.ReadByte()
		from++
	}

	return from
}

func (cc *ChatConn) MergeOutputBuffer(newb []byte) []byte {
	old_n := cc.OutputBuffer.Len()
	if old_n == 0 {
		return newb
	}

	new_n := len(newb)
	all_n := old_n + new_n
	all_b := make([]byte, all_n)
	cc.OutputBuffer.Read(all_b)
	copy(all_b[old_n:], newb)

	return all_b
}

// implement net.Conn interface
func (cc *ChatConn) Read(b []byte) (int, error) {
	from := cc.ReadFromBuffer(b, 0)
	if from == len(b) {
		return from, nil
	}

	messageType, p, err := cc.Conn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage {
		return from, err
	}

	_, err = cc.InputBuffer.Write(p)
	if err != nil {
		return from, err
	}

	err = cc.InputBuffer.WriteByte('\n')
	if err != nil {
		return from, err
	}

	from = cc.ReadFromBuffer(b, from)
	return from, nil
}

func (cc *ChatConn) Write(newb []byte) (int, error) {
	b := cc.MergeOutputBuffer(newb)

	n := len(b)
	var from int
	var to int
	for to < n {
		if b[to] == '\n' {
			if to-from > 0 {
				if err := cc.Conn.WriteMessage(websocket.TextMessage, b[from:to+1]); err != nil {
					break
				}
			}

			from = to + 1
		}

		to++
	}

	if from < n {
		cc.OutputBuffer.Write(b[from:])
	}

	return len(newb), nil
}

func (cc *ChatConn) Close() error {
	return cc.Conn.Close()
}

func (cc *ChatConn) LocalAddr() net.Addr {
	return cc.Conn.LocalAddr()
}

func (cc *ChatConn) RemoteAddr() net.Addr {
	return cc.Conn.RemoteAddr()
}

func (cc *ChatConn) SetDeadline(t time.Time) (err error) {
	if err = cc.Conn.SetReadDeadline(t); err == nil {
		err = cc.Conn.SetWriteDeadline(t)
	}
	return
}

func (cc *ChatConn) SetReadDeadline(t time.Time) error {
	return cc.Conn.SetReadDeadline(t)
}

func (cc *ChatConn) SetWriteDeadline(t time.Time) error {
	return cc.Conn.SetWriteDeadline(t)
}

// implement net.Conn interface
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  512,
	WriteBufferSize: 512,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func websocketHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	wsConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Method not allowed", 405)
		return
	}
	chatServer.OnNewConnection(&ChatConn{Conn: wsConn})
}

func createWebsocketServer(port int) {
	log.Printf("Websocket listening at :%d ...\n", port)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("public"))))
	http.HandleFunc("/ws", websocketHandler)
	http.HandleFunc("/", httpHandler)

	address := fmt.Sprintf(":%d", port)
	if err := http.ListenAndServe(address, nil); err != nil {
		log.Fatal("Websocket server failt to start: ", err)
	}
}

func createSocketServer(port int) {
	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("General socket listen error: %s\n", err.Error())
	}
	log.Printf("General socket listening at %s: ...\n", listener.Addr())

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("General socket accept new connection error: %s\n", err.Error())
			return
		}
		chatServer.OnNewConnection(conn)
	}
}

var chatServer *internal.Server
func main() {

	chatServer = internal.CreateChatServer()

	go createSocketServer(9981)

	createWebsocketServer(6636)
}
