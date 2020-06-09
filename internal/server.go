package internal

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"regexp"
	_ "runtime"
	"strings"
	"time"
)

type Server struct {
	Rooms    map[string]*Room
	Lobby    *Room
	Visitors map[string]*Visitor
	ToExit   chan int

	PendingConnections chan net.Conn
	ChangeRoomRequests chan *Visitor
	ChangeNameRequests chan *Visitor

	RegexpBraces *regexp.Regexp
}

func (s *Server) CreateMessage(who string, what string) string {
	return fmt.Sprintf("[%s] %s> %s\n", time.Now().Local().Format("15:04:05"), who, what)
}

func (s *Server) NormalizeName(name string) string {
	return s.RegexpBraces.ReplaceAllString(name, "")
}

func (s *Server) CreateRandomVisitorName() string {
	for {
		name := fmt.Sprintf("visitor_%d", 10000+rand.Intn(9990000))
		if s.Visitors[strings.ToLower(name)] == nil {
			return name
		}
	}
}

func (s *Server) run() {
	rand.Seed(time.Now().UTC().UnixNano())

	s.Rooms = make(map[string]*Room)
	s.Lobby = s.newRoom(LobbyRoomID)
	s.Visitors = make(map[string]*Visitor)
	s.ToExit = make(chan int, 1)

	s.PendingConnections = make(chan net.Conn, MaxPendingConnections)
	s.ChangeRoomRequests = make(chan *Visitor, MaxBufferedChangeRoomRequests)
	s.ChangeNameRequests = make(chan *Visitor, MaxBufferedChangeNameRequests)

	s.RegexpBraces = regexp.MustCompile("[{}]")

	go s.Lobby.run()

	go func(server *Server) {
		for {
			select {
			case conn := <-server.PendingConnections:
				visitor := server.newVisitor(conn, server.CreateRandomVisitorName())
				if visitor != nil {
					visitor.OutputMessages <- server.CreateMessage(
						"Server",
						fmt.Sprintf("your name: %s. You can input /name new_name to change your name.",
							visitor.Name))
					go visitor.run()
				}
			case visitor := <-server.ChangeNameRequests:
				visitor.changeName()
			case visitor := <-server.ChangeRoomRequests:
				switch {
				case visitor.CurrentRoom != nil:
					visitor.CurrentRoom.VisitorLeaveRequests <- visitor
				case visitor.NextRoomID == VoidRoomID:
					visitor.endChangingRoom()
					log.Printf("Destroy visitor: %s", visitor.Name)
					server.destroyVisitor(visitor)
				default:
					room := server.Rooms[strings.ToLower(visitor.NextRoomID)]
					if room == nil {
						room = server.newRoom(visitor.NextRoomID)
						go room.run()
					}
					room.VisitorEnterRequests <- visitor
				}
			}
		}
	}(s)

	<-s.ToExit
}

func (s *Server) OnNewConnection(conn net.Conn) {
	s.PendingConnections <- conn
}

func CreateChatServer() (server *Server) {
	server = &Server{}
	go server.run()
	return server
}
