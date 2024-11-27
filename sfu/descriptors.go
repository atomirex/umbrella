package sfu

import "github.com/pion/webrtc/v4"

func trackKindToWebrtcKind(kind TrackKind) webrtc.RTPCodecType {
	switch kind {
	case TrackKind_Audio:
		return webrtc.RTPCodecTypeAudio
	case TrackKind_Video:
		return webrtc.RTPCodecTypeVideo
	}

	return webrtc.RTPCodecTypeUnknown
}

func trackKindFromWebrtcKind(kind webrtc.RTPCodecType) TrackKind {
	switch kind {
	case webrtc.RTPCodecTypeAudio:
		return TrackKind_Audio
	case webrtc.RTPCodecTypeVideo:
		return TrackKind_Video
	}

	return TrackKind_Unknown
}
