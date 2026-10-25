// Package protocol defines the JSON messages exchanged over the WebSocket.
// See docs/protocol.md; client/src/scripts/protocol.ts mirrors these types.
package protocol

// Inbound is any message sent by a client. Which fields are used depends on Type.
type Inbound struct {
	Type  string `json:"type"`            // hello | join | room | peekRoom | enter | start | toRoom | cancel | move | rematch | leave
	Token string `json:"token,omitempty"` // hello
	Name  string `json:"name,omitempty"`  // join, room, enter
	Code  string `json:"code,omitempty"`  // peekRoom, enter
	X     int    `json:"x"`               // move
	Y     int    `json:"y"`               // move
}

// Simple is an outbound message with no payload: lobby, queued, kicked.
type Simple struct {
	Type string `json:"type"`
}

// Room is a room for playing with friends, sent to the players waiting in it
// whenever it changes.
type Room struct {
	Type    string       `json:"type"` // room
	Code    string       `json:"code"`
	Host    string       `json:"host"`    // the host's name; they may still be in the last game
	Players []RoomMember `json:"players"` // waiting for the next game, in joining order
	You     int          `json:"you"`     // index into Players
	Max     int          `json:"max"`
	Min     int          `json:"min"`     // players needed before the host can start
	Playing bool         `json:"playing"` // a game is in progress; Players join the next one
}

type RoomMember struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Host      bool   `json:"host"`
}

// RoomInvite describes a room, in reply to peekRoom.
type RoomInvite struct {
	Type    string   `json:"type"` // roomInvite
	Code    string   `json:"code"`
	Host    string   `json:"host"`
	Players []string `json:"players"` // names of those waiting in the room
	Max     int      `json:"max"`
	Playing bool     `json:"playing"` // a game is in progress; you'd join the next one
}

type Welcome struct {
	Type  string `json:"type"` // welcome
	Token string `json:"token"`
	Name  string `json:"name"`
}

type Error struct {
	Type    string `json:"type"`           // error
	Code    string `json:"code,omitempty"` // "name": username rejected; "room": room link unusable or room closed
	Message string `json:"message"`
}

// Stats is sent to players on the home and matchmaking screens.
type Stats struct {
	Type   string `json:"type"`   // stats
	Online int    `json:"online"` // players in a running game or waiting in the queue
}

// Cell is an [x, y] coordinate pair.
type Cell [2]int

// Move is a mark placed on the board: [x, y, seat], where seat indexes
// Game.Players.
type Move [3]int

// Player is one seat of a match. Seats play X, O, T, S in order, and X moves
// first.
type Player struct {
	Name      string `json:"name"`
	Mark      string `json:"mark"`
	Connected bool   `json:"connected"`
	Out       string `json:"out,omitempty"` // timeout | resign: dropped out of a running game
	Left      bool   `json:"left"`          // left the finished game for good; no rematch with them
	Rematch   bool   `json:"rematch"`       // asked for a rematch
}

// Game is the full state of a match from one player's point of view. It is
// sent whenever anything about the match changes.
type Game struct {
	Type        string   `json:"type"` // game
	ID          string   `json:"id"`
	Mode        string   `json:"mode"` // random | room
	You         int      `json:"you"`  // your seat: index into Players
	Players     []Player `json:"players"`
	Moves       []Move   `json:"moves"` // in play order
	Turn        string   `json:"turn"`  // mark
	RemainingMs int64    `json:"remainingMs"`
	TurnMs      int64    `json:"turnMs"`
	Over        bool     `json:"over"`
	Winner      string   `json:"winner,omitempty"` // mark; empty after a draw
	Reason      string   `json:"reason,omitempty"` // five | timeout | resign | draw
	Line        []Cell   `json:"line,omitempty"`
	Room        string   `json:"room,omitempty"` // games with friends: the room's code while you can go back to it
}
