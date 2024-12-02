package sfu

import (
	"log"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// World's most hacked together flow control.
// 1. Aggressively nack the incoming stream (this is to ensure the SFU has the data to forward on)

type receivedSenderReport struct {
	report *rtcp.SenderReport
	at     time.Time
}

type receivedRtpPacket struct {
	pkt *rtp.Packet

	sequenceNumber int
}

// Fairly naive structure for doing this - need to get some sort of flow control working before optimizing it
type packetRing struct {
	pkts []*receivedRtpPacket

	maxTimestamp      uint32
	maxTimestampIndex int

	addedCount int

	sequenceNumberCycles int // The number of times the seq number has rolled over - used to apply real sequence numbers

	maxSequenceNumber             int
	allGoodUpToThisSequenceNumber int // Known everything is ok up to this sequence number, but if it drifts to more than 200 off then it is reset
}

func newPacketRing() *packetRing {
	return &packetRing{
		pkts:                          make([]*receivedRtpPacket, 1024),
		allGoodUpToThisSequenceNumber: -1,
	}
}

func (pr *packetRing) wrapIndex(n int) int {
	for ; n < 0; n += 1024 {
	}

	return n & 1023
}

func (pr *packetRing) add(p *receivedRtpPacket) {
	index := pr.wrapIndex(int(p.pkt.Header.SequenceNumber))
	existing := pr.pkts[index]

	// Apply real sequence number!
	if int(p.pkt.Header.SequenceNumber) < ((pr.maxSequenceNumber & 65535) - 32768) {
		if existing == nil || existing.pkt.Timestamp < p.pkt.Timestamp {
			// Rollover!
			pr.sequenceNumberCycles++
		}
	}

	p.sequenceNumber = (pr.sequenceNumberCycles << 16) | int(p.pkt.SequenceNumber)

	if pr.allGoodUpToThisSequenceNumber == -1 {
		// It's our first packet!
		pr.allGoodUpToThisSequenceNumber = p.sequenceNumber
	}

	if existing != nil {
		// Check it's newer and/or not the same
		if p.pkt.Header.Timestamp > existing.pkt.Timestamp {
			pr.pkts[index] = p
			pr.addedCount++
		}
	} else {
		pr.pkts[index] = p
		pr.addedCount++
	}

	if p.pkt.Timestamp > pr.maxTimestamp {
		pr.maxTimestamp = p.pkt.Timestamp
		pr.maxTimestampIndex = index
	}

	// See if this value needs updating
	allgood := true
	previous := pr.pkts[pr.wrapIndex(pr.allGoodUpToThisSequenceNumber)]
	for i := pr.allGoodUpToThisSequenceNumber + 1; allgood; i++ {
		nextIndex := pr.wrapIndex(i)
		next := pr.pkts[nextIndex]
		if next != nil && next.sequenceNumber == previous.sequenceNumber+1 {
			pr.allGoodUpToThisSequenceNumber = next.sequenceNumber
		} else {
			allgood = false
		}
	}

	if p.sequenceNumber > pr.maxSequenceNumber {
		pr.maxSequenceNumber = p.sequenceNumber
	}

	if pr.maxSequenceNumber-pr.allGoodUpToThisSequenceNumber > 200 {
		// Skip all those bad packets and go again
		pr.allGoodUpToThisSequenceNumber = pr.maxSequenceNumber
	}
}

func (pr *packetRing) evaluateMissingPackets() ([]uint16, bool) {
	// Very stupid one!
	// Completely ignores time . . .
	// Should really know how far back to go, but also have some sense of if we're expecting things which haven't arrived

	missing := make([]uint16, 0)

	max := pr.maxSequenceNumber - 20 // Give it some leeway for what is in flight
	for i := pr.allGoodUpToThisSequenceNumber + 1; i < max; i++ {
		index := pr.wrapIndex(i)
		existing := pr.pkts[index]
		if existing == nil || existing.sequenceNumber != i {
			missing = append(missing, uint16((i & 65535)))
		}
	}

	return missing, len(missing) > 0
}

func fanoutIncoming(c *client, intrack *incomingTrack, s *Sfu) {
	defer s.removeOutgoingTracksForIncomingTrack(intrack)

	bufSize := 32768

	if intrack.remote.Kind() == webrtc.RTPCodecTypeVideo {
		bufSize = bufSize * 8
	}

	buf := make([]byte, bufSize)

	pktchan := make(chan *receivedRtpPacket, 4)
	defer close(pktchan)

	rtcpoutchan := make(chan []rtcp.Packet, 24)
	defer close(rtcpoutchan)

	// RTCP in!
	go func() {
		r := intrack.receiver
		var lastSenderReport *receivedSenderReport

		for {
			pkts, _, err := r.ReadRTCP()
			if err != nil {
				return
			}

			for _, p := range pkts {
				if sr, ok := p.(*rtcp.SenderReport); ok {
					rsr := &receivedSenderReport{report: sr, at: time.Now()}

					if lastSenderReport != nil {
						// log.Println(intrack.descriptor.Kind, intrack.UmbrellaID(), "New SR! Time since last: ", rsr.at.Sub(lastSenderReport.at))
					}

					lastSenderReport = rsr
				}
			}
		}
	}()

	// RTCP out towards incoming
	go func() {
		for {
			pkts, ok := <-rtcpoutchan
			if !ok {
				return
			}

			err := c.incoming.WriteRTCP(pkts)
			if err != nil {
				log.Println("Error writing RTCP", err)
			}
		}
	}()

	go func() {
		pr := newPacketRing()
		t := time.NewTicker(time.Millisecond * 20)
		defer t.Stop()

		for {
			select {
			case pkt, ok := <-pktchan:
				if !ok {
					return
				}

				pr.add(pkt)

				// TODO inject things in here
				// A per outgoing PC track that we can write to separately
				// Mainly in response to incoming NACK alerts
				//
				// BE CAREFUL OF PION INTERNALS MODIFYING pkt.pkt

				if err := intrack.relay.WriteRTP(pkt.pkt); err != nil {
					c.logger.Error(c.label, "Error writing rtp from "+intrack.String()+" to relay "+err.Error())
					return
				}
			case <-t.C:
				if missing, ok := pr.evaluateMissingPackets(); ok {

					pairs := rtcp.NackPairsFromSequenceNumbers(missing)
					rtcpoutchan <- []rtcp.Packet{&rtcp.TransportLayerNack{
						SenderSSRC: uint32(intrack.remote.RtxSSRC()),
						MediaSSRC:  uint32(intrack.remote.SSRC()),
						Nacks:      pairs,
					}}
				}
			}
		}
	}()

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

		pktchan <- &receivedRtpPacket{
			pkt: rtpPkt.Clone(), // Have to clone it because something modifies it otherwise
		}
	}
}
