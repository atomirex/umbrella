package sfu

import (
	"atomirex.com/umbrella/razor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

type PeerConnection struct {
	label   string
	wrapped *webrtc.PeerConnection
	logger  *razor.Logger

	OnICECandidate             func(w *webrtc.ICECandidate)
	OnICEConnectionStateChange func(is webrtc.ICEConnectionState)
	OnSignalingStateChange     func(ss webrtc.SignalingState)
	OnConnectionStateChange    func(pcs webrtc.PeerConnectionState)
	OnTrack                    func(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver)
	OnNegotiationNeeded        func()
}

type PeerConnectionFactory interface {
	NewPeerConnection(label string) (*PeerConnection, error)
}

type PionPeerConnectionFactory struct {
	webrtcApi *webrtc.API
	pcConfig  *webrtc.Configuration
	logger    *razor.Logger
}

func (p *PionPeerConnectionFactory) NewPeerConnection(label string) (*PeerConnection, error) {
	pc, err := p.webrtcApi.NewPeerConnection(*p.pcConfig)

	if err != nil {
		return nil, err
	}

	newPc := &PeerConnection{
		label:   label,
		wrapped: pc,
		logger:  p.logger,
	}

	pc.OnICECandidate(func(i *webrtc.ICECandidate) {
		p.logger.Verbose(label, "Ice candidate")
		if newPc.OnICECandidate != nil {
			newPc.OnICECandidate(i)
		}
	})

	pc.OnSignalingStateChange(func(ss webrtc.SignalingState) {
		p.logger.Info(label, "OnSignalingStateChange"+ss.String())
		if newPc.OnSignalingStateChange != nil {
			newPc.OnSignalingStateChange(ss)
		}
	})

	pc.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		p.logger.Info(label, "OnICEConnectionStateChange"+is.String())
		if newPc.OnICEConnectionStateChange != nil {
			newPc.OnICEConnectionStateChange(is)
		}
	})

	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		p.logger.Info(label, "OnConnectionStateChange"+pcs.String())
		if newPc.OnConnectionStateChange != nil {
			newPc.OnConnectionStateChange(pcs)
		}
	})

	pc.OnTrack(func(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
		p.logger.Info(label, "OnTrack")
		if newPc.OnTrack != nil {
			newPc.OnTrack(tr, r)
		}
	})

	pc.OnNegotiationNeeded(func() {
		p.logger.Info(label, "OnNegotiationNeeded")
		if newPc.OnNegotiationNeeded != nil {
			newPc.OnNegotiationNeeded()
		}
	})

	return newPc, nil
}

func (pc *PeerConnection) GetStatus() *SFUStatusPeerConnection {
	return &SFUStatusPeerConnection{
		ConnectionState:    pc.wrapped.ConnectionState().String(),
		SignalingState:     pc.wrapped.SignalingState().String(),
		IceConnectionState: pc.wrapped.ICEConnectionState().String(),
		IceGatheringState:  pc.wrapped.ICEGatheringState().String(),

		TransceiverCount: int32(len(pc.wrapped.GetTransceivers())),
		SenderCount:      int32(len(pc.wrapped.GetSenders())),
		ReceiverCount:    int32(len(pc.wrapped.GetReceivers())),
	}
}

func (pc *PeerConnection) Close() error {
	pc.logger.Info(pc.label, "Closing")

	err := pc.wrapped.Close()

	pc.logger.NilErrCheck(pc.label, "Error when closing", err)

	return err
}

func (pc *PeerConnection) CreateDataChannel(name string, params *webrtc.DataChannelInit) (*webrtc.DataChannel, error) {
	return pc.wrapped.CreateDataChannel(name, params)
}

func (pc *PeerConnection) GetReceivers() []*webrtc.RTPReceiver {
	return pc.wrapped.GetReceivers()
}

func (pc *PeerConnection) GetSenders() []*webrtc.RTPSender {
	return pc.wrapped.GetSenders()
}

func (pc *PeerConnection) GetTransceivers() []*webrtc.RTPTransceiver {
	return pc.wrapped.GetTransceivers()
}

func (pc *PeerConnection) WriteRTCP(pkts []rtcp.Packet) error {
	return pc.wrapped.WriteRTCP(pkts)
}

func (pc *PeerConnection) AddTransceiverFromKind(kind webrtc.RTPCodecType, params webrtc.RTPTransceiverInit) (*webrtc.RTPTransceiver, error) {
	return pc.wrapped.AddTransceiverFromKind(kind, params)
}

func (pc *PeerConnection) IsTerminated() bool {
	switch pc.wrapped.ConnectionState() {
	case webrtc.PeerConnectionStateClosed:
	case webrtc.PeerConnectionStateDisconnected:
	case webrtc.PeerConnectionStateFailed:
		return true
	}

	return false
}

func (pc *PeerConnection) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return pc.wrapped.AddICECandidate(candidate)
}

func (pc *PeerConnection) CreateAnswer(options *webrtc.AnswerOptions) (webrtc.SessionDescription, error) {
	return pc.wrapped.CreateAnswer(options)
}

func (pc *PeerConnection) CreateOffer(options *webrtc.OfferOptions) (webrtc.SessionDescription, error) {
	return pc.wrapped.CreateOffer(options)
}

func (pc *PeerConnection) SetRemoteDescription(description webrtc.SessionDescription) error {
	return pc.wrapped.SetRemoteDescription(description)
}

func (pc *PeerConnection) SetLocalDescription(description webrtc.SessionDescription) error {
	return pc.wrapped.SetLocalDescription(description)
}

func (pc *PeerConnection) AddTrack(track webrtc.TrackLocal) (*webrtc.RTPSender, error) {
	return pc.wrapped.AddTrack(track)
}

func (pc *PeerConnection) RemoveTrack(sender *webrtc.RTPSender) error {
	return pc.wrapped.RemoveTrack(sender)
}
