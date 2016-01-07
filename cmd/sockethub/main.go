package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/googollee/go-socket.io"
	"github.com/rs/cors"
)

type Room struct {
	Metadata interface{}
	Clients  map[string]*Client
}

type Client struct {
	Id       string      `json:"id"`
	Metadata interface{} `json:"metadata"`
	Socket   socketio.Socket
	Rooms    map[string]*Room
}

type Brain struct {
	Rooms   map[string]*Room
	Clients map[string]*Client
}

func (brain *Brain) GetRoom(name string) *Room {
	if room, found := brain.Rooms[name]; found {
		return room
	}

	room := Room{
		Clients: make(map[string]*Client),
	}
	brain.Rooms[name] = &room
	return &room
}

func (brain *Brain) GetClient(socket socketio.Socket) *Client {
	if client, found := brain.Clients[socket.Id()]; found {
		return client
	}

	client := Client{
		Id:     socket.Id(),
		Socket: socket,
		Rooms:  make(map[string]*Room),
	}
	brain.Clients[socket.Id()] = &client
	return &client
}

func (brain *Brain) Join(so socketio.Socket, roomName string) (*Room, error) {
	if len(roomName) < 1 {
		return nil, fmt.Errorf("invalid room name %q", roomName)
	}

	room := brain.GetRoom(roomName)
	if room == nil {
		return nil, fmt.Errorf("failed to get room %q", roomName)
	}

	client := brain.GetClient(so)
	if client == nil {
		return nil, fmt.Errorf("failed to get client %q", so.Id())
	}

	if err := client.Socket.Join(roomName); err != nil {
		return nil, err
	}
	room.Clients[client.Socket.Id()] = client
	client.Rooms[roomName] = room
	//return nil, nil
	return room, nil
}

func (brain *Brain) Leave(so socketio.Socket, roomName string) error {
	client := brain.GetClient(so)
	if client == nil {
		return fmt.Errorf("failed to get client %q", so.Id())
	}

	if room, found := client.Rooms[roomName]; found {
		delete(client.Rooms, roomName)
		delete(room.Clients, so.Id())
	} else {
		return fmt.Errorf("user %q not in room %q", so.Id(), roomName)
	}
	return nil
}

func (brain *Brain) RemoveClient(so socketio.Socket) error {
	client := brain.GetClient(so)
	if client == nil {
		return fmt.Errorf("no such client %q", so.Id())
	}

	for roomName := range client.Rooms {
		brain.Leave(so, roomName)
	}

	delete(brain.Clients, so.Id())
	return nil
}

var brain *Brain

func NewBrain() *Brain {
	brain := Brain{
		Clients: make(map[string]*Client),
		Rooms:   make(map[string]*Room),
	}
	return &brain
}

func init() {
	brain = NewBrain()
}

func main() {
	server, err := socketio.NewServer(nil)
	if err != nil {
		logrus.Fatalf("Failed to initialize new Socket.io server: %v", err)
	}

	server.On("connection", func(so socketio.Socket) {
		// motd
		so.Emit("message", "welcome-on-server")

		// Socket.io events
		so.On("disconnection", func() {
			logrus.Infof("on disconnect")

			client := brain.GetClient(so)
			for roomName := range client.Rooms {
				so.BroadcastTo(roomName, "message", "client-disconnected", so.Id())
			}

			brain.RemoveClient(so)
		})

		// protocol
		so.On("ping", func() {
			so.Emit("pong")
		})

		so.On("room-join", func(roomName string) {
			room, err := brain.Join(so, roomName)
			if err != nil {
				logrus.Errorf("Failed to join room %q: %v", roomName, err)
			}

			// Join on Socket.IO
			so.Join(roomName)
			so.Emit("message", "welcome-to-room", roomName, room.Metadata)

			// broadcast client infos
			client := brain.GetClient(so)
			so.BroadcastTo(roomName, "message", "new-room-client", so.Id(), client.Metadata)
		})

		so.On("room-broadcast", func(roomName string, msg ...interface{}) {
			so.BroadcastTo(roomName, "message", "broadcast-from", so.Id(), msg)
		})

		so.On("client-set-metadata", func(metadata ...interface{}) {
			client := brain.GetClient(so)
			client.Metadata = metadata
			for roomName := range client.Rooms {
				so.BroadcastTo(roomName, "message", "client-metadata-update", so.Id(), metadata)
			}
		})

		so.On("room-set-metadata", func(roomName string, metadata ...interface{}) {
			room := brain.GetRoom(roomName)

			// FIXME: check if user is in the room

			// Set metadata
			room.Metadata = metadata

			// Broadcast the change
			so.BroadcastTo(roomName, "message", "room-metadata-update", roomName, metadata)
		})

		so.On("room-leave", func(roomName string) {
			so.BroadcastTo(roomName, "message", "client-leave", so.Id())

			brain.Leave(so, roomName)
			// FIXME: check error
		})

		so.On("room-get-users", func(roomName string) {
			room := brain.GetRoom(roomName)

			users := map[string]interface{}{}
			for _, client := range room.Clients {
				users[client.Id] = client.Metadata
			}

			so.Emit("message", "room-users", users)
		})

		so.On("stats", func() {
			so.Emit("message", "statistics", "FIXME")
		})
	})

	server.On("error", func(so socketio.Socket, err error) {
		logrus.Errorf("error: %v", err)
	})

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(assetFS())))
	mux.Handle("/socket.io/", server)

	c := cors.New(cors.Options{
		AllowCredentials: true,
	})
	handler := c.Handler(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "5000"
	}
	logrus.Infof("Serving at :%s", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), handler); err != nil {
		logrus.Fatalf("http error: %v", err)
	}
}
