package sfu

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

type incomingTrack struct {
	descriptor *TrackDescriptor
	remote     *webrtc.TrackRemote
	relay      *webrtc.TrackLocalStaticRTP
	receiver   *webrtc.RTPReceiver
}

func (it *incomingTrack) String() string {
	return fmt.Sprintf("{IncomingTrack id: %s}", it.descriptor.UmbrellaId)
}

type incomingTrackWithClientState struct {
	track *incomingTrack

	transceiverMid string // The mid of the transceiver when the track is received, until then ""
}

func (it *incomingTrackWithClientState) String() string {
	return fmt.Sprintf("{IncomingTrackWithClientState id: %s mid: %s}", it.track.descriptor.UmbrellaId, it.transceiverMid)
}

type outgoingTrack struct {
	descriptor *TrackDescriptor
}

func (ot *outgoingTrack) String() string {
	return fmt.Sprintf("{OutgoingTrack id: %s}", ot.descriptor.UmbrellaId)
}

type outgoingTrackWithClientState struct {
	track *outgoingTrack

	source         *incomingTrack
	remoteNotified bool // Indicates we have sent upstreamTracks including this track to the remote device
	remoteAccepted bool // Indicates we have received confirmation this track is expected by the remote device
}

func (ot *outgoingTrackWithClientState) String() string {
	return fmt.Sprintf("{CutgoingTrackWithClientState id: %s}", ot.track.descriptor.UmbrellaId)
}

// This points to the definition of descriptor being wrong
// i.e. the descriptor needs to include separate fields for the mid, the id, the src mid etc.
// then need to create an "incomingID" of clientid-srcmid for each track
func (t *incomingTrack) UmbrellaID() string {
	return t.descriptor.UmbrellaId
}

func (t *outgoingTrack) UmbrellaID() string {
	return t.descriptor.UmbrellaId
}

func (t *incomingTrackWithClientState) UmbrellaID() string {
	return t.track.UmbrellaID()
}

func (t *outgoingTrackWithClientState) UmbrellaID() string {
	return t.track.UmbrellaID()
}
