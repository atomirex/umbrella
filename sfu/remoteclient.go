package sfu

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"atomirex.com/umbrella/razor"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"google.golang.org/protobuf/proto"
)

type clientCommand int

const (
	clientSendProto = iota
	clientHandleWSMessage
	clientStop
	clientAddOutgoingTrackForIncomingTrack
	clientRemoveOutgoingTracksForIncomingTrack
	clientKeyframeTick
	clientKeyframeGateUnlock
	clientEvalState
	clientDialWs
	clientIncomingTrackAdded
	clientGetStatus
)

type rawIncomingTrack struct {
	track    *webrtc.TrackRemote
	receiver *webrtc.RTPReceiver
}

type clientCommandMessage struct {
	message          *RemoteNodeMessage
	incomingTrack    *incomingTrack
	newincomingTrack *rawIncomingTrack
	result           *clientCommandResult
}

type clientCommandResult struct {
	status chan *SFUStatusClient
}

type client struct {
	label  string // Descriptive label to help tracing
	logger *razor.Logger

	trunkurl  string // Empty means this is a normal web client - should replace with proper roles or similar
	incoming  *PeerConnection
	outgoing  *PeerConnection
	websocket *websocket.Conn

	// The umbrellaId -> incomingTrack
	incomingTracks map[string]*incomingTrackWithClientState

	// The umbrellaId -> outgoingTrack
	outgoingTracks map[string]*outgoingTrackWithClientState

	// The umbrellaId -> RTPSender
	senders map[string]*webrtc.RTPSender

	handler *razor.MessageHandler[clientCommand, clientCommandMessage]

	// mid <-> umbrella track ID mappings - only set when known to be valid!
	incomingMidToUmbrellaTrackID map[string]string

	// Incoming tracks that have yet to be attached to MIDs
	stagedIncomingTracks []*rawIncomingTrack
}

func (c *client) getStatus() *SFUStatusClient {
	msg := clientCommandMessage{result: &clientCommandResult{
		status: make(chan *SFUStatusClient, 1),
	}}

	c.handler.Send(clientGetStatus, &msg)

	return <-msg.result.status
}

func (c *client) run(ws *websocket.Conn, s *Sfu) {
	c.incomingTracks = make(map[string]*incomingTrackWithClientState)
	c.outgoingTracks = make(map[string]*outgoingTrackWithClientState)

	c.incomingMidToUmbrellaTrackID = make(map[string]string)

	c.stagedIncomingTracks = make([]*rawIncomingTrack, 0)

	c.senders = make(map[string]*webrtc.RTPSender)

	incoming, err := s.peerConnectionFactory.NewPeerConnection(fmt.Sprintf("incoming for %s", c.label))
	if c.logger.NilErrCheck(c.label, "Failed to create an incoming peer connection", err) {
		return
	}

	outgoing, err := s.peerConnectionFactory.NewPeerConnection(fmt.Sprintf("outgoing for %s", c.label))
	if c.logger.NilErrCheck(c.label, "Failed to create an outgoing peer connection", err) {
		return
	}

	c.incoming = incoming
	c.outgoing = outgoing

	falseptr := false

	_, err = incoming.CreateDataChannel("data-out", &webrtc.DataChannelInit{Ordered: &falseptr})
	c.logger.NilErrCheck(c.label, "Failed to create an incoming data channel", err)

	_, err = outgoing.CreateDataChannel("data-in", &webrtc.DataChannelInit{Ordered: &falseptr})
	c.logger.NilErrCheck(c.label, "Failed to create an outgoing data channel", err)

	_, err = c.outgoing.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	})
	c.logger.NilErrCheck(c.label, "Error adding fake transceiver to outgoing pc", err)

	// keyframe throttling
	sendKeyFrameGate := false

	c.handler = razor.NewMessageHandler(c.logger, c.label, 1024, func(what clientCommand, payload *clientCommandMessage) bool {
		shouldSendKeyframe := false
		shouldEvalState := false

		stop := func() {
			c.logger.Info(c.label, "Stopping")

			c.handler.Abort()

			c.logger.Info(c.label, "Stopped")
		}

		switch what {
		case clientKeyframeTick:
			shouldSendKeyframe = true
		case clientKeyframeGateUnlock:
			sendKeyFrameGate = false
		case clientSendProto:
			if c.websocket == nil {
				c.logger.Error(c.label, "Attempting send when not connected to websocket")
				return true
			}

			data, err := proto.Marshal(payload.message)

			if err != nil {
				c.logger.Error(c.label, "Failed to marshal proto for client")
				stop()
				return true
			}

			c.logger.Verbose(c.label, "ws proto sending "+payload.message.String())

			err = c.websocket.WriteMessage(websocket.BinaryMessage, data)

			if c.logger.NilErrCheck(c.label, "Failed to write proto to ws", err) {
				stop()
				return true
			}
		case clientHandleWSMessage:
			c.handleWsMessage(payload.message, s)
		case clientStop:
			stop()
			return true
		case clientAddOutgoingTrackForIncomingTrack:
			// Add it to our outgoing if it's not on incoming
			if _, incomingExists := c.incomingTracks[payload.incomingTrack.UmbrellaID()]; !incomingExists {
				c.outgoingTracks[payload.incomingTrack.UmbrellaID()] = &outgoingTrackWithClientState{
					track:  &outgoingTrack{descriptor: payload.incomingTrack.descriptor},
					source: payload.incomingTrack,
				}

				c.handler.Cancel(clientEvalState)
				c.handler.Timeout(clientEvalState, nil, 500*time.Millisecond)
			}
		case clientRemoveOutgoingTracksForIncomingTrack:
			if _, exists := c.outgoingTracks[payload.incomingTrack.UmbrellaID()]; exists {
				delete(c.outgoingTracks, payload.incomingTrack.UmbrellaID())

				c.handler.Cancel(clientEvalState)
				c.handler.Timeout(clientEvalState, nil, 500*time.Millisecond)
			}
		case clientEvalState:
			if c.pcTerminated() {
				stop()
				return true
			}

			shouldEvalState = true
		case clientGetStatus:
			// Like the SFU this is horribly blocking
			// Also could cause problems if the client goes AWOL while the SFU is wanting to get status (i.e. would need a timeout)

			intd := make([]*TrackDescriptor, 0)
			for _, t := range c.incomingTracks {
				intd = append(intd, t.track.descriptor)
			}
			outtd := make([]*TrackDescriptor, 0)
			for _, t := range c.outgoingTracks {
				outtd = append(outtd, t.track.descriptor)
			}
			senderStatus := make([]*SFUStatusSender, 0)
			for umbrellaId, s := range c.senders {
				t := s.Track()
				id := ""
				if t != nil {
					id = t.ID()
				}
				senderStatus = append(senderStatus, &SFUStatusSender{HasTrack: t != nil, TrackIdIfSet: id, UmbrellaId: umbrellaId})
			}
			midMapping := make([]*MidToUmbrellaIDMapping, 0)
			for mid, umbrellaId := range c.incomingMidToUmbrellaTrackID {
				midMapping = append(midMapping, &MidToUmbrellaIDMapping{Mid: mid, UmbrellaId: umbrellaId})
			}
			stagedIncoming := make([]*SFUStatusStagedIncomingTrack, 0)
			for _, s := range c.stagedIncomingTracks {
				stagedIncoming = append(stagedIncoming, &SFUStatusStagedIncomingTrack{
					StreamId: s.track.StreamID(),
					TrackId:  s.track.ID(),
					Mid:      s.receiver.RTPTransceiver().Mid(),
				})
			}

			status := &SFUStatusClient{
				Label:                c.label,
				TrunkUrl:             c.trunkurl,
				IncomingPC:           c.incoming.GetStatus(),
				OutgoingPC:           c.outgoing.GetStatus(),
				IncomingTracks:       intd,
				OutgoingTracks:       outtd,
				Senders:              senderStatus,
				MidMapping:           midMapping,
				StagedIncomingTracks: stagedIncoming,
			}

			payload.result.status <- status
		case clientDialWs:
			if c.websocket != nil {
				return true
			}

			dialer := websocket.Dialer{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // TODO remove this bad ignoring of certs, but useful for local testing

				NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						port = "443" // Default wss port
					} else {
						// Use our own mdns to discover it, if possible
						// This is to enable an OpenWrt AP to discover Apple servers on the same subnet
						if strings.HasSuffix(host, ".local") {
							found, err := s.mdnsLookup(host)
							if err == nil {
								host = found
							}
						}
					}

					dialer := &net.Dialer{}
					return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
				},
			}

			conn, _, err := dialer.Dial(c.trunkurl, nil)
			if c.logger.NilErrCheck(c.label, "Error dialling ws "+c.trunkurl, err) {
				c.handler.Timeout(clientDialWs, nil, time.Second*10)
				return true
			}

			c.websocket = conn
			go func() {
				c.continueWebsocket(s)
			}()
		case clientIncomingTrackAdded:
			t := payload.newincomingTrack.track
			tsc := payload.newincomingTrack.receiver.RTPTransceiver()

			c.logger.Info(c.label, "Incoming track to add stream id "+t.StreamID()+" "+t.ID()+" MID "+tsc.Mid())

			c.stagedIncomingTracks = append(c.stagedIncomingTracks, payload.newincomingTrack)

			c.evalIncomingState(s)
		}

		if shouldEvalState {
			c.handler.Cancel(clientEvalState)

			shouldSendKeyframe = true
			c.evalState(s)
		}

		if shouldSendKeyframe {
			// Throttled
			if !sendKeyFrameGate {
				c.handler.Cancel(clientKeyframeTick)

				sendKeyFrameGate = true
				c.handler.Timeout(clientKeyframeGateUnlock, nil, time.Millisecond*500)

				for _, receiver := range c.incoming.GetReceivers() {
					if receiver.Track() != nil {
						_ = c.incoming.WriteRTCP([]rtcp.Packet{
							&rtcp.PictureLossIndication{
								MediaSSRC: uint32(receiver.Track().SSRC()),
							},
						})
					}
				}

				c.handler.Timeout(clientKeyframeTick, nil, time.Second*3)
			}
		}

		return true
	})

	if ws == nil {
		c.handler.Send(clientDialWs, nil)
	}

	c.handler.Loop(func() {
		for _, it := range c.incomingTracks {
			s.removeOutgoingTracksForIncomingTrack(it.track)
		}

		if ws != nil {
			ws.Close()
			ws = nil
		}

		if outgoing != nil {
			outgoing.wrapped.GracefulClose()
		}
		outgoing = nil

		if incoming != nil {
			incoming.wrapped.GracefulClose()
		}
		incoming = nil

		c.logger.Info(c.label, "Post clean up finished")
	})
}

func newTrunkingClient(logger *razor.Logger, trunkurl string, s *Sfu) *client {
	c := &client{
		label:    fmt.Sprintf("Trunking client to %s", trunkurl),
		logger:   logger,
		trunkurl: trunkurl,
	}

	c.run(nil, s)

	return c
}

func newClient(logger *razor.Logger, ws *websocket.Conn, s *Sfu) *client {
	c := &client{
		label:     fmt.Sprintf("Incoming client from %s", ws.UnderlyingConn().RemoteAddr()),
		logger:    logger,
		websocket: ws,
	}

	c.run(ws, s)

	return c
}

func (c *client) stop() {
	c.handler.CancelAll()
	c.handler.Send(clientStop, nil)
}

func (c *client) pcTerminated() bool {
	return c.incoming.IsTerminated() || c.outgoing.IsTerminated()
}

func (c *client) fanoutIncoming(intrack *incomingTrack, s *Sfu) {
	defer s.removeOutgoingTracksForIncomingTrack(intrack)

	bufSize := 32768

	if intrack.remote.Kind() == webrtc.RTPCodecTypeVideo {
		bufSize = bufSize * 8
	}

	buf := make([]byte, bufSize)
	rtpPkt := &rtp.Packet{}

	for {
		i, _, err := intrack.remote.Read(buf)
		if c.logger.NilErrCheck(c.label, "Fan out error reading rtp on track "+intrack.String(), err) {
			return
		}

		if err = rtpPkt.Unmarshal(buf[:i]); err != nil {
			c.logger.Error(c.label, "Error unmarshaling rtp on track "+intrack.String()+" "+err.Error())
			return
		}

		rtpPkt.Extension = false
		rtpPkt.Extensions = nil

		if err = intrack.relay.WriteRTP(rtpPkt); err != nil {
			c.logger.Error(c.label, "Error writing rtp from "+intrack.String()+" to relay "+err.Error())
			return
		}
	}
}

func (c *client) handleWsMessage(message *RemoteNodeMessage, s *Sfu) {
	if message.Candidate != nil {
		c.logger.Info(c.label, "WS PROTO RECEIVED ice candidate")
		candidate := webrtc.ICECandidateInit{}
		if err := json.Unmarshal([]byte(message.Candidate.Candidate), &candidate); err != nil {
			c.logger.Error(c.label, "Failed to unmarshal json to candidate: "+err.Error())
			return
		}

		c.logger.Info(c.label, "Got candidate: "+candidate.Candidate)

		pc := c.incoming
		// Incoming from pov of the sender!
		if message.Candidate.Incoming {
			pc = c.outgoing
		}

		if err := pc.AddICECandidate(candidate); err != nil {
			c.logger.Error(c.label, "Failed to add ICE candidate: "+err.Error())
			return
		}
	}

	if message.Answer != nil {
		c.logger.Info(c.label, "WS PROTO RECEIVED answer")
		answer := webrtc.SessionDescription{}
		if err := json.Unmarshal([]byte(message.Answer.Answer), &answer); err != nil {
			c.logger.Error(c.label, "Failed to umarshal JSON to answer: "+err.Error())
			return
		}

		c.logger.Info(c.label, "Got answer: "+answer.SDP)

		if err := c.outgoing.SetRemoteDescription(answer); err != nil {
			c.logger.Error(c.label, "Failed to set remote description on outgoing from answer: "+err.Error())
			return
		}
	}

	if message.Offer != nil {
		c.logger.Info(c.label, "WS PROTO RECEIVED offer")
		offer := webrtc.SessionDescription{}
		if err := json.Unmarshal([]byte(message.Offer.Offer), &offer); err != nil {
			c.logger.Error(c.label, "Failed to umarshal JSON to offer: "+err.Error())
			return
		}

		c.logger.Info(c.label, "Got offer: "+offer.SDP)

		if err := c.incoming.SetRemoteDescription(offer); err != nil {
			c.logger.Error(c.label, "Failed to set remote description on incoming from offer: "+err.Error())
			return
		}

		answer, err := c.incoming.CreateAnswer(&webrtc.AnswerOptions{})
		if err != nil {
			c.logger.Error(c.label, "Failed to create answer in response to offer: "+err.Error())
			return
		}

		c.incoming.SetLocalDescription(answer)

		answerString, err := json.Marshal(answer)
		if err != nil {
			c.logger.Error(c.label, "Failed to marshal answer to json: "+err.Error())
			return
		}

		c.writeProto(&RemoteNodeMessage{Answer: &AnswerMessage{Answer: string(answerString)}})
	}

	if message.AcceptTracks != nil {
		c.logger.Info(c.label, "WS PROTO RECEIVED accept tracks "+message.AcceptTracks.String())

		// Mark the accepted tracks and schedule an evaluation
		for _, td := range message.AcceptTracks.Tracks {
			ot := c.outgoingTracks[td.UmbrellaId]
			if ot != nil {
				ot.remoteNotified = true
				ot.remoteAccepted = true
			}
		}

		c.handler.Send(clientEvalState, nil)
	}

	if message.UpstreamTracks != nil {
		c.logger.Info(c.label, "WS PROTO RECEIVED upstream tracks "+message.UpstreamTracks.String())

		// Any previously unknown upstream tracks need to have transceivers created for them
		// Then we acknowledge with the complete set of expected upstream tracks
		for _, td := range message.UpstreamTracks.Tracks {
			_, exists := c.incomingTracks[td.UmbrellaId]
			if !exists {
				if td.Kind != TrackKind_Unknown {
					c.logger.Info(c.label, "Adding transceiver "+td.Id+" "+td.Kind.String())

					_, err := c.incoming.AddTransceiverFromKind(trackKindToWebrtcKind(td.Kind), webrtc.RTPTransceiverInit{
						Direction: webrtc.RTPTransceiverDirectionRecvonly,
					})

					if err != nil {
						c.logger.Error(c.label, "Failed to add transceiver "+err.Error())
					} else {
						intrack := &incomingTrackWithClientState{
							track: &incomingTrack{descriptor: td},
						}

						c.incomingTracks[intrack.UmbrellaID()] = intrack
					}
				}
			}
		}

		acceptedTracks := make([]*TrackDescriptor, 0)
		for _, intrack := range c.incomingTracks {
			acceptedTracks = append(acceptedTracks, intrack.track.descriptor)
		}

		c.writeProto(&RemoteNodeMessage{AcceptTracks: &AcceptUpstreamTracks{Tracks: acceptedTracks}})

		s.handler.Send(sfuSignalClients, nil)
	}

	if message.MidMappings != nil {
		c.logger.Info(c.label, "WS PROTO RECEIVED mid <-> umbrella mapping "+message.MidMappings.String())
		// Review all incoming tracks to assign MIDs, and if newly so then fan out appropriately

		// Update all the mappings in the client
		for _, mapping := range message.MidMappings.Mapping {
			c.incomingMidToUmbrellaTrackID[mapping.Mid] = mapping.UmbrellaId
		}

		c.evalIncomingState(s)
	}
}

func (c *client) evalIncomingState(s *Sfu) {
	c.logger.Info(c.label, "Eval incoming state")

	// Review staged incoming tracks to see if any can be updated as a result
	for i := 0; i < len(c.stagedIncomingTracks); i++ {
		sit := c.stagedIncomingTracks[i]

		mid := sit.receiver.RTPTransceiver().Mid()
		if mid == "" {
			continue
		}

		umbrellaId, midKnown := c.incomingMidToUmbrellaTrackID[mid]
		if midKnown {
			intrack, trackExists := c.incomingTracks[umbrellaId]
			if trackExists {
				if intrack.track.remote == nil {
					c.logger.Info(c.label, "Ready to fan out track: "+umbrellaId)

					// Assign it, remove from staged, and launch fan out
					intrack.track.remote = sit.track

					intrack.track.descriptor.Id = sit.track.ID()
					intrack.track.descriptor.StreamId = sit.track.StreamID()
					intrack.transceiverMid = mid

					// Remove from staged
					c.stagedIncomingTracks = append(c.stagedIncomingTracks[:i], c.stagedIncomingTracks[i+1:]...)
					i--

					relay, err := webrtc.NewTrackLocalStaticRTP(intrack.track.remote.Codec().RTPCodecCapability, "UMB_RELAY"+uuid.New().String(), intrack.track.remote.StreamID())
					if err != nil {
						panic(err)
					}

					intrack.track.relay = relay

					go c.fanoutIncoming(intrack.track, s)

					s.handler.Send(sfuAddOutgoingTracksForIncomingTrack, &sfuCommandMessage{intrack: intrack.track})
					c.logger.Info(c.label, "New incoming track sent to SFU: "+umbrellaId)
				}
			}
		}
	}
}

func (c *client) evalState(s *Sfu) {
	// Do we have any non attached tracks we should be uploading which have not yet been sent for confirmation?
	needsNotification := false
	for _, ot := range c.outgoingTracks {
		needsNotification = needsNotification || !ot.remoteNotified
	}

	// If so, send them up, return and wait before evalling again
	if needsNotification {
		notifications := make([]*TrackDescriptor, 0)
		for _, ot := range c.outgoingTracks {
			notifications = append(notifications, ot.track.descriptor)
			ot.remoteNotified = true
		}

		c.writeProto(&RemoteNodeMessage{UpstreamTracks: &SetUpstreamTracks{Tracks: notifications}})

		c.handler.Cancel(clientEvalState)
		c.handler.Timeout(clientEvalState, nil, 300*time.Millisecond) // Should probably do this a better way, like in the event listener
		return
	}

	// If we have any we're waiting on confirmation of we should return and wait before evalling again
	needsConfirmation := false
	for _, ot := range c.outgoingTracks {
		needsConfirmation = needsConfirmation || !ot.remoteAccepted
	}

	if needsConfirmation {
		// Wait for the accept tracks event

		c.handler.Cancel(clientEvalState)
		c.handler.Timeout(clientEvalState, nil, 300*time.Millisecond) // Should probably do this a better way, like in the event listener
		return
	}

	ss := c.outgoing.wrapped.SignalingState()
	if ss != webrtc.SignalingStateStable {
		c.handler.Cancel(clientEvalState)
		c.handler.Timeout(clientEvalState, nil, 300*time.Millisecond) // Should probably do this a better way, like in the event listener
		return
	}

	senderRemovalFailed := false
	// Find any senders that don't have a track, and remove them
	for umbrellaId, sender := range c.senders {
		_, exists := c.outgoingTracks[umbrellaId]
		if !exists {
			c.logger.Debug(c.label, "eval state removing sender for track with umb id "+umbrellaId)

			if err := c.outgoing.RemoveTrack(sender); err != nil {
				c.logger.Error(c.label, "Error removing sender for track with umb id "+umbrellaId+" "+err.Error())
				senderRemovalFailed = true
			} else {
				delete(c.senders, umbrellaId)
			}
		}
	}

	if senderRemovalFailed {
		// Retry again later
		c.handler.Cancel(clientEvalState)
		c.handler.Timeout(clientEvalState, nil, 300*time.Millisecond)
		return
	}

	addingTrackFailed := false
	// Find any tracks which don't have a sender, and add them
	for umbrellaId, ot := range c.outgoingTracks {
		sender, exists := c.senders[umbrellaId]
		if !exists || sender.Track() == nil {
			c.logger.Debug(c.label, "eval state creating sender for track with umb id "+umbrellaId)
			if sender, err := c.outgoing.AddTrack(ot.source.relay); err != nil {
				c.logger.Error(c.label, "Error creating sender for track with umb id "+umbrellaId+" "+err.Error())
				addingTrackFailed = true
			} else {
				c.senders[umbrellaId] = sender
			}
		}
	}

	if addingTrackFailed {
		// Retry again later
		c.handler.Cancel(clientEvalState)
		c.handler.Timeout(clientEvalState, nil, 300*time.Millisecond)
		return
	}

	c.logger.Info(c.label, "eval state creating offer")
	offer, err := c.outgoing.CreateOffer(nil)
	if c.logger.NilErrCheck(c.label, "eval state creating offer error", err) {
		// Retry again later
		c.handler.Cancel(clientEvalState)
		c.handler.Timeout(clientEvalState, nil, 300*time.Millisecond)
		c.logger.Info(c.label, "eval state creating offer erred so retry scheduled")
		return
	}

	c.logger.Info(c.label, "eval state setting local description")
	if err = c.outgoing.SetLocalDescription(offer); err != nil {
		c.logger.Error(c.label, "eval state setting local description error"+err.Error())
		// Retry again later
		c.handler.Cancel(clientEvalState)
		c.handler.Timeout(clientEvalState, nil, 300*time.Millisecond)
		c.logger.Info(c.label, "eval state setting local description erred so retry scheduled")
		return
	}

	offerString, err := json.Marshal(offer)
	if c.logger.NilErrCheck(c.label, "Eval state offer marshalling failed", err) {
		return
	}

	c.logger.Info(c.label, "Send offer to client: "+offer.SDP)

	c.writeProto(&RemoteNodeMessage{
		Offer: &OfferMessage{
			Offer: string(offerString),
		},
	})
}

func (c *client) writeProto(m *RemoteNodeMessage) {
	c.handler.Send(clientSendProto, &clientCommandMessage{message: m})
}

func (c *client) continueWebsocket(s *Sfu) {
	// This takes care of the various cleanup
	defer func() {
		c.stop()
	}()

	ws := c.websocket
	incoming := c.incoming
	outgoing := c.outgoing

	s.handler.Send(sfuAddClient, &sfuCommandMessage{client: c})

	defer s.handler.Send(sfuRemoveClient, &sfuCommandMessage{client: c})

	icecandidate := func(i *webrtc.ICECandidate, incoming bool) {
		if i == nil {
			return
		}

		// If you are serializing a candidate make sure to use ToJSON
		// Using Marshal will result in errors around `sdpMid` (thanks pion commenter!)
		candidateString, err := json.Marshal(i.ToJSON())

		if c.logger.NilErrCheck(c.label, "Failed to marshal candidate to json", err) {
			return
		}

		c.writeProto(&RemoteNodeMessage{
			Candidate: &CandidateMessage{
				Candidate: string(candidateString),
				Incoming:  incoming,
			},
		})
	}

	incoming.OnICECandidate = func(i *webrtc.ICECandidate) {
		icecandidate(i, true)
	}

	outgoing.OnICECandidate = func(i *webrtc.ICECandidate) {
		icecandidate(i, false)
	}

	incoming.OnConnectionStateChange = func(p webrtc.PeerConnectionState) {
		switch p {
		case webrtc.PeerConnectionStateFailed:
		case webrtc.PeerConnectionStateDisconnected:
		case webrtc.PeerConnectionStateClosed:
			c.stop()
		default:
		}

		s.handler.Send(sfuSignalClients, nil)
	}

	outgoing.OnConnectionStateChange = func(p webrtc.PeerConnectionState) {
		switch p {
		case webrtc.PeerConnectionStateFailed:
		case webrtc.PeerConnectionStateDisconnected:
		case webrtc.PeerConnectionStateClosed:
			c.stop()
		default:
		}

		s.handler.Send(sfuSignalClients, nil)
	}

	outgoing.OnSignalingStateChange = func(ss webrtc.SignalingState) {
		if ss == webrtc.SignalingStateStable {
			mappings := make([]*MidToUmbrellaIDMapping, 0)

			for umbrellaId := range c.outgoingTracks {
				sender, exists := c.senders[umbrellaId]
				if exists && sender.Track() != nil {
					for _, trx := range outgoing.GetTransceivers() {
						if trx.Sender() == sender {
							mappings = append(mappings, &MidToUmbrellaIDMapping{
								Mid:        trx.Mid(),
								UmbrellaId: umbrellaId,
							})
						}
					}
				}
			}

			c.writeProto(&RemoteNodeMessage{MidMappings: &MidToUmbrellaIDMappings{Mapping: mappings}})
		}
	}

	incoming.OnTrack = func(t *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
		c.logger.Info(c.label, "OnTrack "+t.ID()+" "+r.RTPTransceiver().Mid())
		c.handler.Send(clientIncomingTrackAdded, &clientCommandMessage{newincomingTrack: &rawIncomingTrack{track: t, receiver: r}})
	}

	incoming.OnNegotiationNeeded = func() {
		c.handler.Send(clientEvalState, nil)
	}

	outgoing.OnNegotiationNeeded = func() {
		c.handler.Send(clientEvalState, nil)
	}

	// Signal for the new PeerConnection
	s.handler.Send(sfuSignalClients, nil)

	for {
		var message RemoteNodeMessage
		_, raw, err := ws.ReadMessage()
		if c.logger.NilErrCheck(c.label, "Failed to read message", err) {
			return
		}

		if err := proto.Unmarshal(raw, &message); err != nil {
			c.logger.Error(c.label, "Failed to unmarshal proto to message: "+err.Error())
			return
		}

		c.handler.Send(clientHandleWSMessage, &clientCommandMessage{message: &message})
	}
}
