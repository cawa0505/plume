// Package Plume provides a tiny WebSocket-based chat server
package plume

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var Environment = os.Getenv("ENV")

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "https://plume.chat" || origin == "http://localhost:4884"
	},
}

type Server struct {
	Room       *Room
	BotClient  *Client
	loginCodes map[string]User
}

func (srv *Server) GenerateLoginCode(u User) string {
	// XXX: panics if uuid gen fails
	// take the first 6 bytes of uuid as token
	token := strings.ToUpper(uuid.New().String()[0:6])
	srv.loginCodes[token] = u

	go func() {
		// valid for 10 minutes
		time.Sleep(10 * time.Minute)

		delete(srv.loginCodes, token)
	}()

	return token
}

func (srv *Server) AuthUser(token string) (User, bool) {
	user, prs := srv.loginCodes[strings.ToUpper(token)]
	return user, prs
}

func (srv *Server) Connect(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	var client *Client
	for {
		var msg Message

		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Printf("error: %v", err)

			if client != nil {
				client.Send("left chat")
			}

			break
		}

		switch msg.Type {
		case MsgHello:
			textParts := strings.Split(msg.Text, "\n")
			if len(textParts) != 2 {
				// malformed hello message
				break
			}

			u := User{
				Name:  textParts[0],
				Email: textParts[1],
			}
			if srv.Room.CanEnter(u) {
				u.SendAuthEmail(srv.GenerateLoginCode(u))
			} else {
				conn.WriteJSON(Message{
					Type: MsgMayNotEnter,
					User: u,
				})
			}
		case MsgAuth:
			token := msg.Text
			u, prs := srv.AuthUser(token)
			if !prs {
				conn.WriteJSON(Message{
					Type: MsgAuthRst,
					User: u,
				})
				break
			}

			client = srv.Room.Enter(u)
			client.OnMessage = func(msg Message) {
				conn.WriteJSON(msg)
			}

			conn.WriteJSON(Message{
				Type: MsgAuthAck,
				User: u,
			})

			log.Printf("@%s entered with email %s", u.Name, u.Email)

			conn.WriteJSON(Message{
				Type: MsgText,
				User: srv.BotClient.User,
				Text: fmt.Sprintf("Hi @%s! Welcome to Plume.chat. You can read more about this project at github.com/thesephist/plume.", u.Name),
			})
			conn.WriteJSON(Message{
				Type: MsgText,
				User: srv.BotClient.User,
				Text: fmt.Sprintf("Please be kind in the chat, and remember that your email (%s) is tied to what you say here. Happy chatting!", u.Email),
			})

			client.Send("entered chat")
		case MsgText:
			if client == nil {
				break
			}

			client.Send(msg.Text)
		default:
			log.Printf("unknown message type: %v", msg)
		}
	}
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	indexFile, err := os.Open("./static/index.html")
	defer indexFile.Close()

	if err != nil {
		io.WriteString(w, "error reading index")
		return
	}

	io.Copy(w, indexFile)
}

func StartServer() {
	r := mux.NewRouter()

	srv := &http.Server{
		Handler:      r,
		Addr:         "127.0.0.1:4884",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}
	plumeSrv := Server{
		Room:       NewRoom(),
		loginCodes: make(map[string]User),
	}

	// Every room gets a bot automatically
	botUser := User{
		Name:  "plumebot",
		Email: "hi@plume.chat",
	}
	plumeSrv.BotClient = plumeSrv.Room.Enter(botUser)

	r.HandleFunc("/", handleHome)
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	r.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
		plumeSrv.Connect(w, r)
	})

	log.Printf("Plume listening on %s\n", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}