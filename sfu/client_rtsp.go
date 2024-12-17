package sfu

import (
	"log"
	"time"

	"atomirex.com/umbrella/razor"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/google/uuid"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// Very experimental rtsp video only ingest
// Serving as basis of client refactor and understanding more about rtsp

type rtspClientCommand int

const (
	rtspClientStop rtspClientCommand = iota
	rtspClientDial
)

type rtspClientCommandMessage struct{}

type RtspClient struct {
	BaseClient

	url     string
	handler *razor.MessageHandler[rtspClientCommand, rtspClientCommandMessage]

	rtsplibClient   *gortsplib.Client
	rtspDescription *description.Session
}

func (r *RtspClient) stop() {
	r.handler.Send(rtspClientStop, nil)
}

func (r *RtspClient) run(s *Sfu) {
	r.handler = razor.NewMessageHandler(r.logger, r.label, 16, func(what rtspClientCommand, payload *rtspClientCommandMessage) bool {
		switch what {
		case rtspClientStop:
			if r.rtsplibClient != nil {
				r.rtsplibClient.Close()
				r.rtsplibClient = nil
			}

			r.handler.Abort()
			return true
		case rtspClientDial:
			if r.rtsplibClient != nil {
				return true
			}

			failedCheck := func(err error) bool {
				if err == nil {
					return false
				}

				log.Println(err)

				if r.rtsplibClient != nil {
					r.rtsplibClient.Close()
					r.rtsplibClient = nil
				}

				r.handler.Timeout(rtspClientDial, nil, 5000*time.Millisecond)
				return true
			}

			u, err := base.ParseURL(r.url)
			if err != nil {
				log.Println("Invalid url", err)
				return true
			}

			r.rtsplibClient = &gortsplib.Client{}
			err = r.rtsplibClient.Start(u.Scheme, u.Host)
			if failedCheck(err) {
				return true
			}

			r.rtspDescription, _, err = r.rtsplibClient.Describe(u)
			if failedCheck(err) {
				return true
			}

			// Video only for now with the eufy
			// Audio breaks things massively, and doesn't work at all
			// Might be best to work out how to use go2rtc libraries
			videointrack := &incomingTrack{
				descriptor: &TrackDescriptor{
					UmbrellaId: "UMB_ID" + uuid.NewString(),
					Kind:       TrackKind_Video,
				},
			}

			videorelay, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
				RTCPFeedback: nil,
			}, "UMB_RSTP_SRC"+uuid.New().String(), "rtsp-src-stream-id"+uuid.NewString())

			videointrack.relay = videorelay

			if failedCheck(err) {
				return true
			}

			go func() {
				defer s.removeOutgoingTracksForIncomingTrack(videointrack)

				// setup all medias
				// this must be called before StartRecording(), since it overrides the control attribute.
				err = r.rtsplibClient.SetupAll(r.rtspDescription.BaseURL, r.rtspDescription.Medias)
				if failedCheck(err) {
					return
				}

				// read RTP packets from the reader and route them to the publisher
				r.rtsplibClient.OnPacketRTPAny(func(medi *description.Media, forma format.Format, pkt *rtp.Packet) {
					if medi.Type == description.MediaTypeVideo {
						p := pkt.Clone()
						p.Extension = false
						p.Extensions = nil

						if err := videointrack.relay.WriteRTP(p); err != nil {
							r.logger.Error(r.label, "Error writing rtp from "+videointrack.String()+" to relay "+err.Error())
							return
						}
					}
				})

				rtcpStop := make(chan struct{})

				go func() {
					// Otherwise it basically never does this!
					keyframehack := time.NewTicker(12 * time.Second)
					defer keyframehack.Stop()

					// Ugh
					defer func() {
						if r := recover(); r != nil {
							log.Println("RTCP panicked", r)
						}
					}()

					for {
						select {
						case _, ok := <-rtcpStop:
							if !ok {
								return
							}
						case <-keyframehack.C:
							err := r.rtsplibClient.WritePacketRTCP(r.rtspDescription.Medias[0], &rtcp.PictureLossIndication{})
							if err != nil {
								return
							}
						}
					}
				}()

				// start playing
				_, err = r.rtsplibClient.Play(nil)
				failedCheck(err)

				failedCheck(r.rtsplibClient.Wait())

				// Properly stop the other goroutines
				// Dislike this, especially needing to check for panics
				close(rtcpStop)
			}()

			s.handler.Send(sfuAddOutgoingTracksForIncomingTrack, &sfuCommandMessage{intrack: videointrack})

			return true
		}
		return true
	})

	r.handler.Send(rtspClientDial, nil)

	r.handler.Loop(func() {
	})
}

func (r *RtspClient) AddOutgoingTracksForIncomingTrack(intrack *incomingTrack) {
	// Do nothing
}

func (r *RtspClient) RemoveOutgoingTracksForIncomingTrack(intrack *incomingTrack) {
	// Do nothing
}

func (r *RtspClient) RequestEvalState() {

}

func (r *RtspClient) getStatus() *SFUStatusClient {
	return nil
}
