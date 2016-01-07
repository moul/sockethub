package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/googollee/go-socket.io"
	"github.com/rs/cors"
)

type PingArgs struct{}

type RoomLeaveArgs struct {
	RoomName string `json:"room"`
}

type RoomMetadataArgs struct {
	RoomName string      `json:"room"`
	Metadata interface{} `json:"metadata"`
}

type RoomBroadcastArgs struct {
	RoomName string      `json:"room-name"`
	Message  interface{} `json:"message"`
}

type ClientSetMetadataArgs struct {
	Metadata interface{} `json:"metadata"`
}

type RoomJoinArgs struct {
	RoomName string `json:"room"`
}

type RoomGetUsersArgs struct {
	RoomName string `json:"room"`
}

type StatsArgs struct{}

type Response struct {
	Event  string      `json:"event"`
	Author string      `json:"author",omitempty`
	Room   string      `json:"room",omitempty`
	Data   interface{} `json:"data",omitempty`
}

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
		logrus.WithFields(logrus.Fields{
			"client": so.Id(),
			"args":   nil,
		}).Info("connection")

		// Initialize the client in brain
		brain.GetClient(so)

		// motd
		so.Emit("message", Response{Event: "welcome-on-server"})

		// Socket.io events
		so.On("disconnection", func() {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   nil,
			}).Info("disconnection")

			client := brain.GetClient(so)
			for roomName := range client.Rooms {
				so.BroadcastTo(roomName, "message", Response{
					Event:  "client-disconnected",
					Author: so.Id(),
				})
			}

			brain.RemoveClient(so)
		})

		// protocol

		so.On("ping", func(args PingArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("ping")
			so.Emit("pong")
		})

		so.On("room-join", func(args RoomJoinArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("room-join")

			room, err := brain.Join(so, args.RoomName)
			if err != nil {
				logrus.Errorf("Failed to join room %q: %v", args.RoomName, err)
			}

			// Join on Socket.IO
			so.Join(args.RoomName)
			so.Emit("message", Response{
				Event: "welcome-to-room",
				Room:  args.RoomName,
				Data:  room.Metadata,
			})

			// broadcast client infos
			client := brain.GetClient(so)
			so.BroadcastTo(args.RoomName, "message", Response{
				Event:  "new-room-client",
				Author: so.Id(),
				Data:   client.Metadata,
				Room:   args.RoomName,
			})
		})

		so.On("room-broadcast", func(args RoomBroadcastArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("room-broadcast")

			so.BroadcastTo(args.RoomName, "message",
				Response{
					Event:  "broadcast-from",
					Author: so.Id(),
					Data:   args.Message,
					Room:   args.RoomName,
				})
		})

		so.On("client-set-metadata", func(args ClientSetMetadataArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("client-set-metadata")

			client := brain.GetClient(so)
			client.Metadata = args.Metadata
			for roomName := range client.Rooms {
				so.BroadcastTo(roomName, "message", Response{
					Event:  "client-metadata-update",
					Author: so.Id(),
					Data:   args.Metadata,
				})
			}
		})

		so.On("room-set-metadata", func(args RoomMetadataArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("room-set-metadata")

			room := brain.GetRoom(args.RoomName)

			// FIXME: check if user is in the room

			// Set metadata
			room.Metadata = args.Metadata

			// Broadcast the change
			so.BroadcastTo(args.RoomName, "message", Response{
				Event:  "room-metadata-update",
				Author: so.Id(),
				Room:   args.RoomName,
				Data:   args.Metadata,
			})
		})

		so.On("room-leave", func(args RoomLeaveArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("room-leave")

			so.BroadcastTo(args.RoomName, "message", Response{
				Event:  "client-leave",
				Author: so.Id(),
				Room:   args.RoomName,
			})

			brain.Leave(so, args.RoomName)
			// FIXME: check error
		})

		so.On("room-get-users", func(args RoomGetUsersArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("room-get-users")

			room := brain.GetRoom(args.RoomName)

			users := map[string]interface{}{}
			for _, client := range room.Clients {
				users[client.Id] = client.Metadata
			}

			so.Emit("message", Response{
				Event: "room-users",
				Data:  users,
				Room:  args.RoomName,
			})
		})

		so.On("stats", func(args StatsArgs) {
			logrus.WithFields(logrus.Fields{
				"client": so.Id(),
				"args":   args,
			}).Info("stats")

			so.Emit("message", Response{
				Event: "statistics",
				Data:  "FIXME",
			})
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
