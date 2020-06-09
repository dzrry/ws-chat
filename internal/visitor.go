package internal

import (
	"bufio"
	"container/list"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
)

type Visitor struct {
	Server *Server

	Connection     net.Conn
	LimitInput     *io.LimitedReader
	Input          *bufio.Reader
	Output         *bufio.Writer
	OutputMessages chan string

	Name     string
	NextName string

	// in CurrentRoom.Visitors
	RoomElement *list.Element
	CurrentRoom *Room
	NextRoomID  string
	RoomChanged chan int

	ReadClosed  chan int
	WriteClosed chan int
	Closed      chan int
}

func (s *Server) newVisitor(c net.Conn, name string) *Visitor {
	visitor := &Visitor{
		Server: s,

		Connection:     c,
		OutputMessages: make(chan string, MaxVisitorBufferedMessages),

		Name:     name,
		NextName: "",

		RoomElement: nil,
		CurrentRoom: nil,
		NextRoomID:  VoidRoomID,
		RoomChanged: make(chan int),

		ReadClosed:  make(chan int),
		WriteClosed: make(chan int),
		Closed:      make(chan int),
	}

	visitor.LimitInput = &io.LimitedReader{c, 0}
	visitor.Input = bufio.NewReader(visitor.LimitInput)
	visitor.Output = bufio.NewWriter(c)

	s.Visitors[strings.ToLower(name)] = visitor

	visitor.beginChangingRoom(LobbyRoomID)

	log.Printf("New visitor: %s", visitor.Name)

	return visitor
}

func (v *Visitor) changeName() {
	server := v.Server

	newName := v.NextName

	rn := []rune(newName)
	if len(rn) < MinVisitorNameLength {
		return
	}

	if len(rn) > MaxVisitorNameLength {
		newName = string(rn[:MaxVisitorNameLength])
	}
	newName = server.NormalizeName(newName)
	if len(newName) < MinVisitorNameLength || len(newName) > MaxVisitorNameLength {
		return
	}

	fmt.Printf("len = %d, min = %d", len(newName), MinVisitorNameLength)

	if server.Visitors[strings.ToLower(newName)] != nil {
		return
	}

	delete(server.Visitors, strings.ToLower(v.Name))
	v.Name = newName
	server.Visitors[strings.ToLower(v.Name)] = v

	v.OutputMessages <- server.CreateMessage(
		"Server",
		fmt.Sprintf("you changed your name to %s", v.Name))
}

func (s *Server) destroyVisitor(v *Visitor) {
	if v.CurrentRoom != nil {
		log.Printf("destroyVisitor: v.CurrentRoom != nil")
	}

	delete(s.Visitors, strings.ToLower(v.Name))

	v.closeConnection()
}

func (v *Visitor) closeConnection() error {
	return v.Connection.Close()
}

func (v *Visitor) beginChangingRoom(newRoomID string) {
	// to block Visitor. Read before room is changed.
	v.RoomChanged = make(chan int)
	v.NextRoomID = newRoomID
	v.Server.ChangeRoomRequests <- v
}

func (v *Visitor) endChangingRoom() {
	v.NextRoomID = VoidRoomID
	close(v.RoomChanged)
}

func (v *Visitor) run() {
	go v.read()
	go v.write()

	<-v.WriteClosed
	<-v.ReadClosed
	close(v.Closed)

	// let server close v
	v.beginChangingRoom(VoidRoomID)
}

func (v *Visitor) read() {
	server := v.Server

	maxNumBytesPerMessage := (MaxMessageLength << 2) + 1
	v.LimitInput.N = int64(maxNumBytesPerMessage)
	inReadingLongMessage := false

	for {
		select {
		case <-v.WriteClosed:
			close(v.ReadClosed)
		case <-v.Closed:
			close(v.ReadClosed)
		default:
		}
		// wait server change room for v, when server has done it, this channel will be closed.
		<-v.RoomChanged

		line, err := v.Input.ReadString('\n')
		if err != nil {
			if err != io.EOF || line == "" {
				close(v.ReadClosed)
			}
		}

		v.LimitInput.N = int64(len(line)) + v.LimitInput.N

		rn := []rune(line)
		switch {
		case len(rn) > MaxMessageLength || len(rn) == MaxMessageLength && line[len(line)-1] != '\n':
			if !inReadingLongMessage {
				v.OutputMessages <- server.CreateMessage("Server", "your message is too long!")
			}
			inReadingLongMessage = line[len(line)-1] != '\n'
			continue
		case inReadingLongMessage:
			inReadingLongMessage = false
			continue
		}
		
		if strings.HasPrefix(line, "/") {
			switch {
			case strings.HasPrefix(line, "/exit"):
				close(v.ReadClosed)
			case strings.HasPrefix(line, "/room"):
				line = strings.TrimPrefix(line, "/room")
				line = strings.TrimSpace(line)
				if len(line) == 0 { // show current room name
					if v.CurrentRoom == nil {
						v.OutputMessages <- server.CreateMessage("Server", "you are in lobby now")
					} else {
						v.OutputMessages <- server.CreateMessage(
							"Server",
							fmt.Sprintf("your are in %s now", v.CurrentRoom.Name))
					}
				} else { // change room
					line = server.NormalizeName(line)

					v.beginChangingRoom(line)
				}
				continue
			case strings.HasPrefix(line, "/name"):
				line = strings.TrimPrefix(line, "/name")
				line = strings.TrimSpace(line)

				if len(line) == 0 {
					v.OutputMessages <- server.CreateMessage(
						"Server",
						fmt.Sprintf("your name is %s", v.Name))
				} else if len(line) >= MinVisitorNameLength && len(line) <= MaxVisitorNameLength {
					v.NextName = line
					server.ChangeNameRequests <- v
				}
				continue
			}
		}

		if v.CurrentRoom != nil {
			if v.CurrentRoom == server.Lobby {
				v.OutputMessages <- server.CreateMessage(
					"Server",
					"you are current in lobby, please input /room room_name to enter a room")
			} else {
				v.CurrentRoom.Messages <- server.CreateMessage(v.Name, line)
			}
		}
	}
}

func (v *Visitor) write() {
	for {
		select {
		case <-v.ReadClosed:
			v.closeConnection()
			close(v.WriteClosed)
		case <-v.Closed:
			v.closeConnection()
			close(v.WriteClosed)
		case message := <-v.OutputMessages:
			_, err := v.Output.WriteString(message)
			if err != nil {
				v.closeConnection()
				close(v.WriteClosed)
			}

			for {
				select {
				case message = <-v.OutputMessages:
					_, err = v.Output.WriteString(message)
					if err != nil {
						v.closeConnection()
						close(v.WriteClosed)
					}
				default:
					if v.Output.Flush() != nil {
						v.closeConnection()
						close(v.WriteClosed)
					}
				}
			}
		}
	}
}
