package sfu

import (
	"fmt"

	"atomirex.com/umbrella/razor"
	"github.com/gorilla/websocket"
)

type RemoteClient interface {
	Label() string
	getStatus() *SFUStatusClient
	stop()
	AddOutgoingTracksForIncomingTrack(*incomingTrack)
	RemoveOutgoingTracksForIncomingTrack(*incomingTrack)
	RequestEvalState()
}

type RemoteClientParameters struct {
	logger   *razor.Logger
	trunkurl string
	s        *Sfu
	ws       *websocket.Conn
}

type RemoteClientFactory interface {
	NewClient(params *RemoteClientParameters) RemoteClient
}

type DefaultRemoteClientFactory struct{}

// This can actually block ever returning . . . thanks to looping on the websocket
func (rcf *DefaultRemoteClientFactory) NewClient(params *RemoteClientParameters) RemoteClient {
	if params.trunkurl == "" {
		c := &client{
			label:     fmt.Sprintf("Incoming client from %s", params.ws.UnderlyingConn().RemoteAddr()),
			logger:    params.logger,
			websocket: params.ws,
		}

		c.run(params.ws, params.s)

		c.continueWebsocket(params.s)

		return c
	} else {
		c := &client{
			label:    fmt.Sprintf("Trunking client to %s", params.trunkurl),
			logger:   params.logger,
			trunkurl: params.trunkurl,
		}

		c.run(nil, params.s)

		return c
	}
}
