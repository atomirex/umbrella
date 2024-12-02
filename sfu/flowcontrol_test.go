package sfu

import (
	"testing"

	"github.com/pion/rtp"
)

func TestSimple(t *testing.T) {
	pr := newPacketRing()

	if pr.addedCount != 0 {
		t.Fatal("Added count wrong 1")
	}

	pr.add(&receivedRtpPacket{
		pkt: &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: 4623,
				Timestamp:      415324,
			},
		},
	})

	if pr.addedCount != 1 {
		t.Fatal("Added count wrong 2")
	}

	if pr.maxTimestamp != 415324 {
		t.Fatal("Max timestamp wrong")
	}

	if pr.maxSequenceNumber != 4623 {
		t.Fatal("Max seqn wrong")
	}

	if pr.allGoodUpToThisSequenceNumber != 4623 {
		t.Fatal("All good wrong")
	}

	missing, ok := pr.evaluateMissingPackets()
	if ok {
		t.Fatal("OK when it's not")
	}

	if len(missing) > 0 {
		t.Fatal("Missing is wrong")
	}
}

func testAllGoodForNPackets(t *testing.T, n int) {
	pr := newPacketRing()

	sequenceNumber := 34636
	timestamp := 325

	for i := 0; i < n; i++ {
		pr.add(&receivedRtpPacket{
			pkt: &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: uint16(sequenceNumber),
					Timestamp:      uint32(timestamp),
				},
			},
		})

		if pr.addedCount != i+1 {
			t.Fatal("Added count wrong when i is", i, "added count is", pr.addedCount)
		}

		if pr.maxTimestamp != uint32(timestamp) {
			t.Fatal("Max timestamp wrong")
		}

		if pr.maxSequenceNumber != sequenceNumber {
			t.Fatal("Max seqn wrong", i, pr.maxSequenceNumber, "expected", sequenceNumber, "cycles", pr.sequenceNumberCycles, "maxincurrentcycles", pr.maxSequenceNumber&65535)
		}

		if pr.allGoodUpToThisSequenceNumber != sequenceNumber {
			t.Fatal("All good wrong")
		}

		missing, ok := pr.evaluateMissingPackets()
		if ok {
			t.Fatal("OK when it's not")
		}

		if len(missing) > 0 {
			t.Fatal("Missing is wrong")
		}

		sequenceNumber++
		timestamp += 35
	}
}

func TestAllGoodFor50Packets(t *testing.T) {
	testAllGoodForNPackets(t, 50)
}
func TestAllGoodFor500Packets(t *testing.T) {
	testAllGoodForNPackets(t, 500)
}

func TestAllGoodFor5000Packets(t *testing.T) {
	testAllGoodForNPackets(t, 5000)
}
func TestAllGoodFor50000Packets(t *testing.T) {
	testAllGoodForNPackets(t, 50000)
}

// TODO test having one missing
// TODO test having a block missing

// TODO both of those, then completely restored

// TODO both of those, then partially restored
