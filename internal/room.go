package internal

import (
	"container/list"
	"fmt"
	"log"
	"strings"
)

type Room struct {
	Server *Server

	ID   string
	Name string

	Visitors *list.List
	Messages chan string

	VisitorLeaveRequests chan *Visitor
	VisitorEnterRequests chan *Visitor
}

func (s *Server) createNewRoom(id string) *Room {
	room := &Room{
		Server: s,

		ID: id,

		Visitors: list.New(),
		Messages: make(chan string, MaxRoomBufferedMessages),

		VisitorLeaveRequests: make(chan *Visitor, MaxRoomCapacity),
		VisitorEnterRequests: make(chan *Visitor, MaxRoomCapacity),
	}

	if id == LobbyRoomID {
		room.Name = id
	} else {
		room.Name = fmt.Sprintf("Room#%s", id)
	}

	s.Rooms[strings.ToLower(id)] = room

	log.Printf("New room: %s", id)

	return room
}

func (r *Room) enterVisitor(v *Visitor) {
	if v.RoomElement != nil || v.CurrentRoom != nil {
		log.Printf("EnterVisitor: v has already entered a r")
	}

	v.CurrentRoom = r
	v.RoomElement = r.Visitors.PushBack(v)
}

func (r *Room) leaveVisitor(v *Visitor) {
	if v.RoomElement == nil || v.CurrentRoom == nil {
		log.Printf("LeaveVisitor: visitor has not entered any room yet")
		return
	}
	if v.CurrentRoom != r {
		log.Printf("LeaveVisitor: visitor.CurrentRoom != room")
		return
	}

	if v != r.Visitors.Remove(v.RoomElement) {
		log.Printf("LeaveVisitor: visitor != element.value")
		return
	}

	v.RoomElement = nil
	v.CurrentRoom = nil
}

func (r *Room) run() {
	server := r.Server

	for {
		select {
		case visitor := <-r.VisitorLeaveRequests:
			r.leaveVisitor(visitor)
			visitor.OutputMessages <- server.CreateMessage(r.Name, "<= you leaved this room.")
			server.ChangeRoomRequests <- visitor
		case visitor := <-r.VisitorEnterRequests:
			if r.Visitors.Len() >= MaxRoomCapacity {
				visitor.OutputMessages <- server.CreateMessage(r.Name, "Sorry, I am full. :(")
			} else {
				r.enterVisitor(visitor)
				visitor.OutputMessages <- server.CreateMessage(r.Name, "<= you entered this room.")
				//r.Messages <- server.CreateMessage (r.Name, fmt.Sprintf ("<= visitor#%s entered this room.", visitor.Name))
			}
			visitor.endChangingRoom()

		case message := <-r.Messages:
			for e := r.Visitors.Front(); e != nil; e = e.Next() {
				visitor, ok := e.Value.(*Visitor)
				if ok {
					visitor.OutputMessages <- message
				}
			}
		}
	}
}
