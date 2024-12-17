import React, { useEffect } from 'react';
import ReactDOM from 'react-dom';
import { useRef, useState } from 'react';
import { SetUpstreamTracks, RemoteNodeMessage, TrackDescriptor, TrackKind, CurrentServers, MidToUmbrellaIDMapping, SFUStatus, SFUStatusClient, SFUStatusPeerConnection } from '../generated/sfu'

function trackKindFromString(k: string) : TrackKind  {
    switch(k) {
        case "audio":
            return TrackKind.Audio;
        case "video":
            return TrackKind.Video;
        default:
            return TrackKind.Unknown;
    }
}

function trackKindToString(k: TrackKind) : string  {
    switch(k) {
        case TrackKind.Audio:
            return "audio";
        case TrackKind.Video:
            return "video";
        default:
            return "unknown";
    }
}

abstract class track {
    private mediaStream: MediaStream;
    private mediaStreamTrack: MediaStreamTrack;
    umbrellaId: string;

    constructor(mediaStream: MediaStream, mediaStreamTrack: MediaStreamTrack, umbrellaId: string) {
        this.mediaStream = mediaStream;
        this.mediaStreamTrack = mediaStreamTrack;
        this.umbrellaId = umbrellaId;
    }

    kind() : TrackKind {
        return trackKindFromString(this.mediaStreamTrack.kind);
    }

    getDescriptor() : TrackDescriptor {
        return ({
            id: this.mediaStreamTrack.id,
            kind: trackKindFromString(this.mediaStreamTrack.kind),
            streamId: this.mediaStream.id,
            umbrellaId: this.umbrellaId,
        });
    }

    getTrack() : MediaStreamTrack {
        return this.mediaStreamTrack;
    }

    getStream() : MediaStream {
        return this.mediaStream;
    }
}

// This is the data associated with an incoming track before we can conclusively move to it being a remote track
class stagedIncomingTrack {
    mediaStream: MediaStream;
    mediaStreamTrack: MediaStreamTrack;
    completed: boolean = false;

    constructor(mediaStream: MediaStream, mediaStreamTrack: MediaStreamTrack) {
        this.mediaStream = mediaStream;
        this.mediaStreamTrack = mediaStreamTrack;
    }
}

class remoteTrack extends track {
}

class localTrack extends track {
    published: boolean;
    private transceiver: RTCRtpTransceiver | null = null;

    constructor(mediaStream: MediaStream, mediaStreamTrack: MediaStreamTrack) {
        super(mediaStream, mediaStreamTrack, "UMB_TRACK_"+crypto.randomUUID());
        this.published = false;
    }

    setTransceiver(transceiver : RTCRtpTransceiver | null) {
        this.transceiver = transceiver;
    }

    getTransceiver() : RTCRtpTransceiver | null {
        return this.transceiver;
    }
}

interface LocalVideoProps {
    stream: MediaStream | null;
}

const LocalVideo: React.FC<LocalVideoProps> = ({ stream }) => {
    const videoRef = useRef<HTMLVideoElement | null>(null);

    useEffect(() => {
        if (videoRef.current && stream) {
            videoRef.current.srcObject = stream;

            const vt = stream.getVideoTracks()[0];
            if(vt) {
                const facingMode = vt.getSettings().facingMode;
                if(facingMode == undefined || facingMode == "user") {
                    videoRef.current.style.transform = "scaleX(-1)";
                } else {
                    videoRef.current.style.transform = "scaleX(1)";
                }
            }
        }
    }, [stream]);

    return <div className='video-container'><video className="local-video" ref={videoRef} autoPlay playsInline /><p className='video-overlay'>Local video</p></div>;
};

interface RemoteVideosProps {
    tracks: remoteTrack[];
}

const RemoteVideos: React.FC<RemoteVideosProps> = ({ tracks }) => {
    return (
        <>
            {tracks.map((track, index) => (
                <RemoteVideo key={index} track={track} />
            ))}
        </>
    );
};

const RemoteVideo: React.FC<{ track: remoteTrack }> = ({ track }) => {
    const videoRef = useRef<HTMLVideoElement | null>(null);

    useEffect(() => {
        if (videoRef.current) {
            videoRef.current.srcObject = track.getStream();
        }
    }, [track]);

    return <div className='video-container'><video key={track.umbrellaId} className="remote-video" ref={videoRef} autoPlay playsInline controls /><p className='video-overlay'>Remote video</p></div>;
};

interface SfuAppJoinedProps {
    requestLocalMediaFirst: boolean;
}

const SfuAppJoined: React.FC<SfuAppJoinedProps> = ({requestLocalMediaFirst}) => {
    const [localStream, setLocalStream] = useState<MediaStream | null>(null);
    const [remoteTracks, setRemoteTracks] = useState<remoteTrack[]>([]);

    const websocketRef = useRef<WebSocket | null>(null);
    const offerNeededTimerRef = useRef<number>(-1);

    useEffect(() => {
        const pageUrl = window.location.href;
        const wsUrl = "wss" + pageUrl.substring(pageUrl.indexOf(":"), pageUrl.lastIndexOf("/")) + "/wsb";
        console.log("Websocket url set to: " + wsUrl);

        function log(msg: string) {
            console.log("SFULOG: "+msg);
        }

        // Always have to ask permission for the streams for webrtc connections to actually work
        // We can safely ignore the streams afterwards
        //
        // The view only one we set because some devices without webcams still benefit from being
        // able to prompt the user (like firefox) due to implementing RFC 8828 with any
        // successful getUserMedia being interpreted as consent for ICE candidate discovery.
        // Without that you will get cryptic errors.
        //
        // You will also get Linux Firefox ICE errors if someone is sending h264 and you don't
        // have the OpenH264 plugin installed and enabled.
        // https://support.mozilla.org/en-US/kb/open-h264-plugin-firefox
        //
        // For now we cap at 720P
        navigator.mediaDevices.getUserMedia({ video: requestLocalMediaFirst ? { width:{max:1280}, height: {max: 720}} : false, audio: true })
        .then(streamInit => {
            const stream = requestLocalMediaFirst ? streamInit : null;

            const pcConfig: RTCConfiguration = {
                iceServers: [
                    {urls: ["stun:stun.l.google.com:19302","stun:stun2.1.google.com:19302"]}
                ]
            };

            const incoming = new RTCPeerConnection(pcConfig);
            const outgoing = new RTCPeerConnection(pcConfig);

            // Gives the offers something to be about
            const dataOut = outgoing.createDataChannel("data-out", {ordered: false});
            const dataIn = incoming.createDataChannel("data-in", {ordered: false});

            const midToUmbrellaIDMapping = new Map<string, string>();
            let stagedIncomingTracks : stagedIncomingTrack[] = [];

            // Should be called whenever the mapping changes or we receive a new track in incoming.ontrack
            // Then if we can fuse the data together the new remotetrack is created
            function evaluateIncomingTracks() {
                const newRemoteTracks : remoteTrack[] = [];

                stagedIncomingTracks.forEach(sit => {
                    const transceiversForIncomingTrack = incoming.getTransceivers().filter((trx) => trx.receiver.track == sit.mediaStreamTrack);
                    if(transceiversForIncomingTrack.length > 0) {
                        console.log("FOUND THESE TRANSCEIVERS: "+JSON.stringify(transceiversForIncomingTrack)+" mid first "+transceiversForIncomingTrack[0].mid);
                        const mid = transceiversForIncomingTrack[0].mid;
                        if(mid != null) {
                            const umbrellaId = midToUmbrellaIDMapping.get(mid);
                            console.log("Umbrella ID with transceiver MID is "+umbrellaId);
                            if(umbrellaId) {
                                newRemoteTracks.push(new remoteTrack(sit.mediaStream, sit.mediaStreamTrack, umbrellaId));
                                sit.completed = true;

                                sit.mediaStreamTrack.onmute = function (event) {
                                    // Hmm
                                };

                                sit.mediaStream.onremovetrack = ({ track }) => {
                                    console.log("Incoming video track removed: "+umbrellaId);
                                    setRemoteTracks((prev) => prev.filter(t => t.umbrellaId !== umbrellaId));
                                };
                            }
                        }
                    }
                });

                stagedIncomingTracks = stagedIncomingTracks.filter(sit => !sit.completed);

                setRemoteTracks((prev) => [...prev, ...newRemoteTracks]);
            }

            incoming.ontrack = (event) => {
                if (event.track.kind === 'audio') {
                    console.log("ONTRACK Don't need to worry about audio");
                    return;
                }

                if(event.streams.length == 0 || event.streams[0] == null) {
                    console.log("ONTRACK provided empty streams");
                    return;
                }

                if (stream != null && stream.id == event.streams[0].id) {
                    console.log("ONTRACK Rejecting due to loopback suspicions "+stream.id+" "+event.streams);
                    // Loopback detected - this doesn't seem right
                    return;
                }

                if(stream != null) {
                    const localVideoTrack = stream.getVideoTracks()[0];
                    console.log("Local video track id "+localVideoTrack.id+" local stream id "+stream.id+" incoming stream id "+event.streams[0].id+" incoming track id "+event.track.id);
                }

                const incomingStream = event.streams[0];
                const incomingTrack = incomingStream.getVideoTracks()[0];

                stagedIncomingTracks.push(new stagedIncomingTrack(incomingStream, incomingTrack));

                evaluateIncomingTracks();
            };

            incoming.onconnectionstatechange = (() => {
                switch(incoming.connectionState) {
                    case "disconnected":
                    case "failed":
                    case "closed":
                        console.log("Incoming connection terminated, state: "+incoming.connectionState);
                        setRemoteTracks([]);
                }
            });

            let ws = new WebSocket(wsUrl);
            websocketRef.current = ws;

            ws.binaryType = "arraybuffer";

            if(stream != null) {
                setLocalStream(stream);
            }

            const localTracks = new Map<string, localTrack>();
            if(stream != null) {
                stream.getTracks().forEach(track => {
                    localTracks.set(track.id, new localTrack(stream, track));
                });
            }

            ws.onopen = e => {
                ws.send(RemoteNodeMessage.toBinary({
                    upstreamTracks: {
                        tracks: Array.from(localTracks.values()).map(track => track.getDescriptor()),
                    },
                }));

                offerNeeded();
            };

            incoming.onicecandidate = e => {
                if (!e.candidate) {
                    return;
                }

                ws.send(RemoteNodeMessage.toBinary({
                    candidate: {
                        candidate: JSON.stringify(e.candidate),
                        incoming: true,
                    },
                }));
            };

            outgoing.onicecandidate = e => {
                if (!e.candidate) {
                    return;
                }

                ws.send(RemoteNodeMessage.toBinary({
                    candidate: {
                        candidate: JSON.stringify(e.candidate),
                        incoming: false,
                    },
                }));
            };

            ws.onclose = function (evt) {
                log("ws.onclose");

                websocketRef.current = null;

                window.alert("Websocket has closed")
            }

            function offerNeeded() {
                if(offerNeededTimerRef.current >= 0) {
                    clearTimeout(offerNeededTimerRef.current);
                }
    
                offerNeededTimerRef.current = setTimeout(async () => {
                    const offer = await outgoing.createOffer();
                    await outgoing.setLocalDescription(offer);
                    
                    if(ws != null) {
                        ws.send(RemoteNodeMessage.toBinary({
                            offer: {
                                offer: JSON.stringify(offer),
                            },
                        }));
                    }
                }, 100);
            }

            outgoing.onsignalingstatechange = () => {
                log("OUTGOING SIGNAL STATE CHANGE "+outgoing.signalingState);

                if(outgoing.signalingState === "stable") {
                    // Review transceivers for local tracks and notify remote server of mappings
                    const mappings : MidToUmbrellaIDMapping[] = [];

                    const trx = outgoing.getTransceivers();
                    localTracks.forEach((lt, id) => {
                        const mst = lt.getTrack();
                        
                        trx.forEach((transceiver) => {
                            if(transceiver.sender.track === mst && transceiver.mid != null) {
                                console.log("FOUND TRANSCEIVER FOR TRACK! mid: "+transceiver.mid);

                                mappings.push({
                                    mid: transceiver.mid,
                                    umbrellaId: lt.umbrellaId,
                                })
                            }
                        });
                    });

                    ws.send(RemoteNodeMessage.toBinary({midMappings: {mapping: mappings}}));
                }
            };

            ws.onmessage = async function (event) {
                if (!event.data) {
                    return;
                }

                let msg = RemoteNodeMessage.fromBinary(new Uint8Array(event.data), { readUnknownField: true });

                log("ws.onmessage: " + JSON.stringify(msg));

                if (msg.offer) {
                    let offer = JSON.parse(msg.offer.offer);
                    
                    incoming.setRemoteDescription(offer);
                    const answer = await incoming.createAnswer();
                    await incoming.setLocalDescription(answer);

                    ws.send(RemoteNodeMessage.toBinary({
                        answer: {
                            answer: JSON.stringify(answer),
                        },
                    }));
                }

                if(msg.answer) {
                    // Outgoing stuff
                    await outgoing.setRemoteDescription(JSON.parse(msg.answer.answer));
                }

                if (msg.candidate) {
                    let candidate = JSON.parse(msg.candidate.candidate);
                    // Incoming from pov of the sender!
                    (msg.candidate.incoming ? outgoing : incoming).addIceCandidate(candidate);
                }

                if (msg.upstreamTracks) {
                    log("Upstream tracks recevied "+JSON.stringify(msg.upstreamTracks));

                    // Just echoing it for now, unlike pion we don't need to get ready
                    ws.send(RemoteNodeMessage.toBinary({acceptTracks: {tracks: msg.upstreamTracks.tracks}}));
                }

                if (msg.midMappings) {
                    log("MID <-> Umbrella mappings received "+JSON.stringify(msg.midMappings.mapping));

                    msg.midMappings.mapping.forEach(m => {
                        midToUmbrellaIDMapping.set(m.mid, m.umbrellaId);
                    });

                    evaluateIncomingTracks();
                }

                if (msg.acceptTracks) {
                    let changed = false;

                    msg.acceptTracks.tracks.forEach(descriptor => {
                        const t = localTracks.get(descriptor.id);

                        if(t) {
                            log("Confirmed track "+JSON.stringify(t.getDescriptor()));
                            if(!t.published) {
                                log("Publishing track "+t.getTrack().id);
                                outgoing.addTrack(t.getTrack(), t.getStream());

                                t.published = true;
                                changed = true;
                            }
                        } else {
                            log("Remote track incoming: "+JSON.stringify(descriptor));
                        }
                    });

                    if(changed) {
                        offerNeeded();
                    }
                }
            }

            ws.onerror = function (evt) {
                log("ws.onerror");
            }
        }).catch(console.log)

        return () => {
            if(offerNeededTimerRef.current >= 0) {
                clearTimeout(offerNeededTimerRef.current);
                offerNeededTimerRef.current = 0;
            }
        };
    }, []);

    const sendWsMessage = (data: Uint8Array) => {
        if(websocketRef.current !== null) {
            websocketRef.current.send(data);
        }
    };

    return (
        <div>
            <div className='centering-container'>
                { requestLocalMediaFirst && <LocalVideo stream={localStream} /> }
                <RemoteVideos tracks={remoteTracks} />
            </div>
        </div>
    );
};

export const SfuApp = () => {
    const [joined, setJoined] = useState<boolean>(false);
    
    const requestLocalMediaFirstRef = useRef<boolean>(false);

    const joinWithLocalMedia = () => {
        requestLocalMediaFirstRef.current = true;
        setJoined(true);
    };

    const joinAsViewer = () => {
        requestLocalMediaFirstRef.current = false;
        setJoined(true);
    };

    return (
        <>
        { joined ? (
            <SfuAppJoined requestLocalMediaFirst={requestLocalMediaFirstRef.current} />
        ) : (
            <div className='centering-container'>
                <button onClick={joinWithLocalMedia}>Join with local camera</button><br />
                <button onClick={joinAsViewer}>Join just as a viewer</button>
            </div>
        ) }
        </>
    )
};

export const ServersApp = () => {
    const [servers, setServers] = useState<string[]>([]);
    const addServerInputRef = useRef<HTMLInputElement | null>(null);

    useEffect(() => {
        fetch(window.location.pathname, {method: 'GET', headers:{'Content-Type': "application/x-protobuf"}})
        .then((response) => response.arrayBuffer())
        .then((buffer) => {
            setServers(CurrentServers.fromBinary(new Uint8Array(buffer)).servers);
            addServerInputRef.current?.focus();
        }).catch(console.log);

        return () => {};
    }, []);

    const addServerClick = () => {
        const updateVal = addServerInputRef.current!.value!;
        const serversUpdate = updateVal === "" ? [] : [updateVal];

        fetch(window.location.pathname, 
            {
                method: 'POST', 
                headers:{'Content-Type': "application/x-protobuf"},
                body: CurrentServers.toBinary({servers:serversUpdate})
            }
        ).then((response) => response.arrayBuffer())
        .then((buffer) => {
            setServers(CurrentServers.fromBinary(new Uint8Array(buffer)).servers);
            addServerInputRef.current?.focus();
        }).catch(console.log);

        addServerInputRef.current!.value = "";
    };

    const removeServerClick = (server: string) => {
        fetch(window.location.pathname, 
            {
                method: 'POST', 
                headers:{'Content-Type': "application/x-protobuf"},
                body: CurrentServers.toBinary({servers: servers.filter((s) => s !== server)})
            }
        ).then((response) => response.arrayBuffer())
        .then((buffer) => {
            setServers(CurrentServers.fromBinary(new Uint8Array(buffer)).servers);
        }).catch(console.log);
    };

    return (
        <>
            <div className='centering-container'>
                <h4>Servers</h4>
                <div style={{ flexBasis: '100%' }}></div>
                <ul>
                { servers.length === 0 ? ( <li>No servers</li>) : (<>
                    { servers.map((server, index) => (
                        <li>{ server } <button onClick={() => { removeServerClick(server) }}>Remove</button></li>
                    )) }
                </>) } 
                </ul>
                <div style={{ flexBasis: '100%' }}></div>
                <input ref={addServerInputRef} type='text' /><button onClick={addServerClick}>Add Server</button>
            </div>
        </>
    )
};

const TrackDescriptorStatusListElement: React.FC<{ descriptor: TrackDescriptor }> = ({descriptor}) => {
    return (
        <li key={descriptor.umbrellaId}>{ descriptor.umbrellaId } <ul>
            <li>Kind {  trackKindToString(descriptor.kind) }</li>
            <li>Track ID { descriptor.id }</li>
            <li>Stream ID { descriptor.streamId }</li>
        </ul></li>
    );
};

const PeerConnectionStatusListElement: React.FC<{ label: string, pc: SFUStatusPeerConnection | undefined }> = ({label, pc}) => {
    return (
        <li key={label}>{ label } <ul>
            <li></li>
        </ul></li>
    );
};

const ClientStatusListElement: React.FC<{ client: SFUStatusClient }> = ({client}) => {
    return (
        <li key={client.label}>{ client.label } <ul>
            <li>Trunk url: {  client.trunkUrl }</li>
            <PeerConnectionStatusListElement label='Incoming PC' pc={client.incomingPC} />
            <PeerConnectionStatusListElement label='Outgoing PC' pc={client.outgoingPC} />
            <li>Incoming tracks<ul>
                { client.incomingTracks.map(t => <TrackDescriptorStatusListElement descriptor={t}/>)}
            </ul></li>
            <li>Outgoing tracks<ul>
                { client.outgoingTracks.map(t => <TrackDescriptorStatusListElement descriptor={t}/>)}
            </ul></li>
            <li>Senders<ul>
                { client.senders.map(s => <li>{s.umbrellaId} { s.hasTrack ? ("Has a track of ID " + s.trackIdIfSet) : "Has no track ID"}</li>)}
            </ul></li>
            <li>MID to Umbrella ID mappings<ul>
                { client.midMapping.map(m => <li>{m.mid} : {m.umbrellaId}</li>)}
            </ul></li>
            <li>Staged incoming tracks<ul>
                { client.stagedIncomingTracks.map(sit => <li>{sit.streamId} {sit.trackId} {sit.mid}</li>)}
            </ul></li>
        </ul></li>
    );
};

export const StatusApp = () => {
    const [status, setStatus] = useState<SFUStatus | null>(null);

    useEffect(() => {
        fetch(window.location.pathname, {method: 'GET', headers:{'Content-Type': "application/x-protobuf"}})
            .then((response) => response.arrayBuffer())
            .then((buffer) => {
                setStatus(SFUStatus.fromBinary(new Uint8Array(buffer)));
            }).catch(console.log);

        return () => {};
    }, []);

    return (
        <>
            <div>
                <h4>Status</h4>
                { (status == null) ? (
                    <p>Status is null</p>
                ) : (
                    <>
                        <h5>Relaying tracks</h5>
                        <ul>
                        {status.relayingTracks.map(td => (
                            <TrackDescriptorStatusListElement descriptor={td} />
                        ))}
                        </ul>
                        <h5>Clients</h5>
                        {status.clients.map(c => (
                            <ClientStatusListElement client={c} />
                        ))}
                        <h5>servers</h5>
                        <ul>
                        {status.servers.map(t => (
                            <li>{ t }</li>
                        ))}
                        </ul>
                    </>
                )}
            </div>
        </>
    )
};