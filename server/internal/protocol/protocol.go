// Package protocol defines the JSON messages exchanged over the WebSocket.
// See docs/protocol.md; client/src/scripts/protocol.ts mirrors these types.
package protocol

// Inbound is any message sent by a client. Which fields are used depends on Type.
type Inbound struct {
	Type  string `json:"type"`            // hello | join | challenge | peek | accept | cancel | move | rematch | leave
	Token string `json:"token,omitempty"` // hello
	Name  string `json:"name,omitempty"`  // join, challenge, accept
	Code  string `json:"code,omitempty"`  // peek, accept
	X     int    `json:"x"`               // move
	Y     int    `json:"y"`               // move
}

// Simple is an outbound message with no payload: lobby, queued, kicked.
type Simple struct {
	Type string `json:"type"`
}

// Challenge tells a player they are waiting for a friend to open their link.
type Challenge struct {
	Type string `json:"type"` // challenge
	Code string `json:"code"`
}

// Invite describes someone else's challenge, in reply to peek.
type Invite struct {
	Type string `json:"type"` // invite
	Code string `json:"code"`
	Name string `json:"name"` // who is challenging
}

type Welcome struct {
	Type  string `json:"type"` // welcome
	Token string `json:"token"`
	Name  string `json:"name"`
}

type Error struct {
	Type    string `json:"type"`           // error
	Code    string `json:"code,omitempty"` // "name": username rejected; "challenge": challenge link unusable
	Message string `json:"message"`
}

// Stats is sent to players on the home and matchmaking screens.
type Stats struct {
	Type   string `json:"type"`   // stats
	Online int    `json:"online"` // players in a running game or waiting in the queue
}

// Cell is an [x, y] coordinate pair.
type Cell [2]int

type Rematch struct {
	You      bool `json:"you"`
	Opponent bool `json:"opponent"`
}

// Game is the full state of a match from one player's point of view. It is
// sent whenever anything about the match changes.
type Game struct {
	Type              string  `json:"type"` // game
	ID                string  `json:"id"`
	Mode              string  `json:"mode"` // random | friend
	You               string  `json:"you"`  // X | O
	YourName          string  `json:"yourName"`
	OpponentName      string  `json:"opponentName"`
	OpponentConnected bool    `json:"opponentConnected"`
	Moves             []Cell  `json:"moves"` // in order; even indexes are X
	Turn              string  `json:"turn"`
	RemainingMs       int64   `json:"remainingMs"`
	TurnMs            int64   `json:"turnMs"`
	Over              bool    `json:"over"`
	Winner            string  `json:"winner,omitempty"`
	Reason            string  `json:"reason,omitempty"` // five | timeout | resign
	Line              []Cell  `json:"line,omitempty"`
	Rematch           Rematch `json:"rematch"`
	OpponentLeft      bool    `json:"opponentLeft"`
}
