package sfu

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"atomirex.com/umbrella/razor"
	"github.com/atomirex/mdns"
	"github.com/gorilla/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

type sfuCommand int

const (
	sfuAddOutgoingTracksForIncomingTrack sfuCommand = iota
	sfuRemoveAllOutgoingTracksForIncomingTrack
	sfuSignalClients
	sfuAddClient
	sfuRemoveClient

	sfuGetCurrentServers
	sfuSetCurrentServers

	sfuGetStatus
)

type sfuCommandMessage struct {
	intrack           *incomingTrack
	client            *client
	SetCurrentServers *CurrentServers

	result *sfuCommandResult
}

type sfuCommandResult struct {
	servers chan *CurrentServers
	status  chan *SFUStatus
}

type Sfu struct {
	clients     []RemoteClient
	sfuCommands chan sfuCommandMessage

	peerConnectionFactory PeerConnectionFactory
	remoteClientFactory   RemoteClientFactory

	// UmbrellaID -> track
	localTracks map[string]*incomingTrack // Set of all incoming tracks which are being relayed

	handler *razor.MessageHandler[sfuCommand, sfuCommandMessage]

	intendedServers map[string]bool // This works because strings completely define the spec of the server right now
	servers         map[string]RemoteClient

	logger *razor.Logger

	loggerPion logging.LeveledLogger

	mdnsConn *mdns.Conn
}

func (s *Sfu) GetStatus() *SFUStatus {
	msg := sfuCommandMessage{result: &sfuCommandResult{
		status: make(chan *SFUStatus, 1),
	}}

	s.handler.Send(sfuGetStatus, &msg)

	return <-msg.result.status
}

func (s *Sfu) GetCurrentServers() *CurrentServers {
	msg := sfuCommandMessage{result: &sfuCommandResult{
		servers: make(chan *CurrentServers, 1),
	}}

	s.handler.Send(sfuGetCurrentServers, &msg)

	return <-msg.result.servers
}

func (s *Sfu) SetCurrentServers(update *CurrentServers) *CurrentServers {
	msg := sfuCommandMessage{
		SetCurrentServers: update,
		result: &sfuCommandResult{
			servers: make(chan *CurrentServers, 1),
		},
	}

	s.handler.Send(sfuSetCurrentServers, &msg)

	return <-msg.result.servers
}

func NewSfu(logger *razor.Logger, minPort uint16, maxPort uint16, ip *string) *Sfu {
	loggerPion := logging.NewDefaultLoggerFactory().NewLogger("sfu-ws")
	loggerPion.(*logging.DefaultLeveledLogger).SetLevel(logging.LogLevelError)

	settingEngine := webrtc.SettingEngine{}

	settingEngine.SetEphemeralUDPPortRange(minPort, maxPort)

	if ip != nil {
		settingEngine.SetNAT1To1IPs([]string{*ip}, webrtc.ICECandidateTypeHost)
	}

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		panic("Error setting default codecs")
	}

	// Now we know what this is and why . . . . facepalm
	// This is the "default" nack, sr, rr etc. handling for rtcp
	// We can come back to it when it's a problem
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, interceptorRegistry); err != nil {
		panic("Panic setting interceptors")
	}

	webrtcApi := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine), webrtc.WithMediaEngine(m))

	s := &Sfu{
		sfuCommands:         make(chan sfuCommandMessage, 256),
		remoteClientFactory: &DefaultRemoteClientFactory{},
		peerConnectionFactory: &PionPeerConnectionFactory{
			pcConfig: &webrtc.Configuration{
				ICEServers: []webrtc.ICEServer{
					{
						URLs: []string{"stun:stun.l.google.com:19302"},
					},
				},
			},
			webrtcApi: webrtcApi,
			logger:    logger,
		},
		localTracks:     make(map[string]*incomingTrack),
		intendedServers: make(map[string]bool),
		servers:         make(map[string]RemoteClient),
		logger:          logger,
		loggerPion:      loggerPion,
	}

	s.handler = razor.NewMessageHandler(logger, "sfu", 1024, func(what sfuCommand, payload *sfuCommandMessage) bool {
		shouldSignalClients := false

		switch what {
		case sfuAddClient:
			s.clients = append(s.clients, payload.client)

			// Add all existing tracks
			for _, t := range s.localTracks {
				payload.client.handler.Send(clientAddOutgoingTrackForIncomingTrack, &clientCommandMessage{incomingTrack: t})
			}

			shouldSignalClients = true
		case sfuRemoveClient:
			index := -1
			for i, c := range s.clients {
				if c == payload.client {
					index = i
				}
			}

			if index >= 0 {
				s.clients = append(s.clients[:index], s.clients[index+1:]...)
			}

			shouldSignalClients = true
		case sfuAddOutgoingTracksForIncomingTrack:
			logger.Info("sfu", "adding track: "+payload.intrack.String())
			intrack := payload.intrack
			s.localTracks[intrack.UmbrellaID()] = intrack

			for _, c := range s.clients {
				c.AddOutgoingTracksForIncomingTrack(intrack)
			}

			shouldSignalClients = true
		case sfuRemoveAllOutgoingTracksForIncomingTrack:
			logger.Info("sfu", "removing all outgoing tracks for track: "+payload.intrack.String())
			delete(s.localTracks, payload.intrack.UmbrellaID())

			for _, c := range s.clients {
				c.RemoveOutgoingTracksForIncomingTrack(payload.intrack)
			}

			shouldSignalClients = true
		case sfuSignalClients:
			shouldSignalClients = true
		case sfuGetStatus:
			// Terrible version of this which blocks while polling everything
			// Should really pass around a thing that gathers the info
			// but in reality I should move to a pub/sub thing that pushes the data out on changes anyway
			// this is to debug the mess as is :P
			logger.Info("sfu", "SFU getting status servers")
			servers := make([]string, 0)
			for t := range s.servers {
				servers = append(servers, t)
			}
			logger.Info("sfu", "SFU getting status relaying")
			relaying := make([]*TrackDescriptor, 0)
			for _, t := range s.localTracks {
				relaying = append(relaying, t.descriptor)
			}
			logger.Info("sfu", "SFU getting status clients")
			clients := make([]*SFUStatusClient, 0)
			for _, c := range s.clients {
				logger.Info("sfu", "SFU getting status for client "+c.Label())
				clients = append(clients, c.getStatus())
				logger.Info("sfu", "SFU received status for client "+c.Label())
			}

			logger.Info("sfu", "SFU getting status returning")
			status := &SFUStatus{
				RelayingTracks: relaying,
				Servers:        servers,
				Clients:        clients,
			}

			payload.result.status <- status
		case sfuGetCurrentServers:
			result := &CurrentServers{Servers: make([]string, 0)}

			for t := range s.servers {
				result.Servers = append(result.Servers, t)
			}

			payload.result.servers <- result
		case sfuSetCurrentServers:
			// "intended" servers model, then regularly evaluate, so setup/teardown repeatedly is ok
			mentioned := make(map[string]bool)
			for _, t := range payload.SetCurrentServers.Servers {
				mentioned[t] = true
			}

			// Just overwrite it with what the client sent! (For now)
			s.intendedServers = mentioned

			s.evaluateServers()

			shouldSignalClients = true

			result := &CurrentServers{Servers: make([]string, 0)}

			for t := range s.servers {
				result.Servers = append(result.Servers, t)
			}

			payload.result.servers <- result
		}

		if shouldSignalClients {
			logger.Verbose("sfu", "SFU should signaling clients")
			s.handler.Cancel(sfuSignalClients)

			for _, c := range s.clients {
				logger.Verbose("sfu", "SFU signaling client "+c.Label())
				c.RequestEvalState()
			}

			logger.Verbose("sfu", "SFU finished signaling clients")
		}

		return true
	})

	s.handler.Loop(func() {
		panic("SFU unexpectedly terminated")
	})

	return s
}

func (s *Sfu) evaluateServers() {
	// Ensure any running servers that should be stopped are stopping or stopped
	for t := range s.servers {
		mention := s.intendedServers[t]
		if !mention {
			s.servers[t].stop()

			// TODO only actually delete the server when it's really stopped (need the get servers endpoint to be pub/sub based first or it'll be useless)
			delete(s.servers, t)
		}
	}

	// Create and start any servers that should exist but do not (i.e. if they are still stopping leave them until stopped and removed)
	for t, _ := range s.intendedServers {
		_, exists := s.servers[t]
		if !exists {
			server := s.remoteClientFactory.NewClient(&RemoteClientParameters{
				logger:   s.logger,
				trunkurl: t,
				s:        s,
			})
			s.servers[t] = server
		}
	}
}

func (s *Sfu) addTrack(intrack *incomingTrack) {
	s.handler.Send(sfuAddOutgoingTracksForIncomingTrack, &sfuCommandMessage{intrack: intrack})
}

func (s *Sfu) removeOutgoingTracksForIncomingTrack(t *incomingTrack) {
	s.handler.Send(sfuRemoveAllOutgoingTracksForIncomingTrack, &sfuCommandMessage{intrack: t})
}

func (s *Sfu) WebsocketHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("sfu", "Failed to upgrade HTTP to Websocket: "+err.Error())
		return
	}

	s.remoteClientFactory.NewClient(&RemoteClientParameters{logger: s.logger, ws: ws, s: s})
}

func (s *Sfu) SetMdnsConn(mdnsConn *mdns.Conn) {
	s.mdnsConn = mdnsConn
}

// Use our own mdns for resolving .local addresses to work around compatibility problems
// If we are in cloud mode this will return an error
// If the host is not found this will return an error
// If the host is found the string returned will contain the IP as a string . . .
func (s *Sfu) mdnsLookup(hostname string) (string, error) {
	if s.mdnsConn == nil {
		return "", fmt.Errorf("attempting to resolve local subnet host while mdns not in use")
	}

	ctx, _ := context.WithTimeout(context.Background(), 1*time.Second)
	_, addr, err := s.mdnsConn.QueryAddr(ctx, hostname)
	if err != nil {
		return "", err
	} else {
		return addr.String(), nil
	}
}
