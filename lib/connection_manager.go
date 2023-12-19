package lib

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/addrmgr"
	chainlib "github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/lru"
	"github.com/deso-protocol/go-deadlock"
	"github.com/golang/glog"
	"github.com/pkg/errors"
)

// connection_manager.go contains most of the logic for creating and managing
// connections with peers. A good place to start is the Start() function.

const (
	// These values behave as -1 when added to a uint. To decrement a uint
	// atomically you need to do use these values.

	// Uint64Dec decrements a uint64 by one.
	Uint64Dec = ^uint64(0)
	// Uint32Dec decrements a uint32 by one.
	Uint32Dec = ^uint32(0)
)

type ConnectionManager struct {
	// Keep a reference to the Server.
	// TODO: I'm pretty sure we can make it so that the ConnectionManager and the Peer
	// doesn't need a reference to the Server object. But for now we keep things lazy.
	srv *Server

	// When --connectips is set, we don't connect to anything from the addrmgr.
	connectIps []string

	// The address manager keeps track of peer addresses we're aware of. When
	// we need to connect to a new outbound peer, it chooses one of the addresses
	// it's aware of at random and provides it to us.
	AddrMgr *addrmgr.AddrManager
	// The interfaces we listen on for new incoming connections.
	listeners []net.Listener
	// The parameters we are initialized with.
	params *DeSoParams
	// The target number of outbound peers we want to have.
	targetOutboundPeers uint32
	// The maximum number of inbound peers we allow.
	maxInboundPeers uint32
	// When true, only one connection per IP is allowed. Prevents eclipse attacks
	// among other things.
	limitOneInboundConnectionPerIP bool

	// When --hypersync is set to true we will attempt fast block synchronization
	HyperSync bool
	// We have the following options for SyncType:
	// - any: Will sync with a node no matter what kind of syncing it supports.
	// - blocksync: Will sync by connecting blocks from the beginning of time.
	// - hypersync-archival: Will sync by hypersyncing state, but then it will
	//   still download historical blocks at the end. Can only be set if HyperSync
	//   is true.
	// - hypersync: Will sync by downloading historical state, and will NOT
	//   download historical blocks. Can only be set if HyperSync is true.
	SyncType NodeSyncType

	// Keep track of the nonces we've sent in our version messages so
	// we can prevent connections to ourselves.
	sentNonces lru.Cache

	// This section defines the data structures for storing all the
	// peers we're aware of.
	//
	// A count of the number active connections we have for each IP group.
	// We use this to ensure we don't connect to more than one outbound
	// peer from the same IP group. We need a mutex on it because it's used
	// concurrently by many goroutines to figure out if outbound connections
	// should be made to particular addresses.

	mtxOutboundConnIPGroups deadlock.Mutex
	outboundConnIPGroups    map[string]int
	// The peer maps map peer ID to peers for various types of peer connections.
	//
	// A persistent peer is typically one we got through a commandline argument.
	// The reason it's called persistent is because we maintain a connection to
	// it, and retry the connection if it fails.
	mtxPeerMaps     deadlock.RWMutex
	persistentPeers map[uint64]*Peer
	outboundPeers   map[uint64]*Peer
	inboundPeers    map[uint64]*Peer
	connectedPeers  map[uint64]*Peer

	// outboundConnectionAttempts keeps track of the outbound connections, mapping attemptId [uint64] -> connection attempt.
	outboundConnectionAttempts map[uint64]*OutboundConnectionAttempt
	// outboundConnectionChan is used to signal successful outbound connections to the connection manager.
	outboundConnectionChan chan *outboundConnection
	// inboundConnectionChan is used to signal successful inbound connections to the connection manager.
	inboundConnectionChan chan *inboundConnection
	// Track the number of outbound peers we have so that this value can
	// be accessed concurrently when deciding whether or not to add more
	// outbound peers.
	numOutboundPeers   uint32
	numInboundPeers    uint32
	numPersistentPeers uint32

	// We keep track of the addresses for the outbound peers so that we can
	// avoid choosing them in the address manager. We need a mutex on this
	// guy because many goroutines will be querying the address manager
	// at once.
	mtxConnectedOutboundAddrs deadlock.RWMutex
	connectedOutboundAddrs    map[string]bool
	attemptedOutboundAddrs    map[string]bool

	// Used to set peer ids. Must be incremented atomically.
	peerIndex    uint64
	attemptIndex uint64

	serverMessageQueue chan *ServerMessage

	// Keeps track of the network time, which is the median of all of our
	// peers' time.
	timeSource chainlib.MedianTimeSource

	// Events that can happen to a peer.
	newPeerChan  chan *Peer
	donePeerChan chan *Peer

	// stallTimeoutSeconds is how long we wait to receive responses from Peers
	// for certain types of messages.
	stallTimeoutSeconds uint64

	minFeeRateNanosPerKB uint64

	// More chans we might want.	modifyRebroadcastInv chan interface{}
	shutdown int32
}

func NewConnectionManager(
	_params *DeSoParams, _addrMgr *addrmgr.AddrManager, _listeners []net.Listener,
	_connectIps []string, _timeSource chainlib.MedianTimeSource,
	_targetOutboundPeers uint32, _maxInboundPeers uint32,
	_limitOneInboundConnectionPerIP bool,
	_hyperSync bool,
	_syncType NodeSyncType,
	_stallTimeoutSeconds uint64,
	_minFeeRateNanosPerKB uint64,
	_serverMessageQueue chan *ServerMessage,
	_srv *Server) *ConnectionManager {

	ValidateHyperSyncFlags(_hyperSync, _syncType)

	return &ConnectionManager{
		srv:        _srv,
		params:     _params,
		AddrMgr:    _addrMgr,
		listeners:  _listeners,
		connectIps: _connectIps,
		// We keep track of the last N nonces we've sent in order to detect
		// self connections.
		sentNonces: lru.NewCache(1000),
		timeSource: _timeSource,

		//newestBlock: _newestBlock,

		// Initialize the peer data structures.
		outboundConnIPGroups:       make(map[string]int),
		persistentPeers:            make(map[uint64]*Peer),
		outboundPeers:              make(map[uint64]*Peer),
		inboundPeers:               make(map[uint64]*Peer),
		connectedPeers:             make(map[uint64]*Peer),
		outboundConnectionAttempts: make(map[uint64]*OutboundConnectionAttempt),
		connectedOutboundAddrs:     make(map[string]bool),
		attemptedOutboundAddrs:     make(map[string]bool),

		// Initialize the channels.
		newPeerChan:            make(chan *Peer, 100),
		donePeerChan:           make(chan *Peer, 100),
		outboundConnectionChan: make(chan *outboundConnection, 100),

		targetOutboundPeers:            _targetOutboundPeers,
		maxInboundPeers:                _maxInboundPeers,
		limitOneInboundConnectionPerIP: _limitOneInboundConnectionPerIP,
		HyperSync:                      _hyperSync,
		SyncType:                       _syncType,
		serverMessageQueue:             _serverMessageQueue,
		stallTimeoutSeconds:            _stallTimeoutSeconds,
		minFeeRateNanosPerKB:           _minFeeRateNanosPerKB,
	}
}

func (cmgr *ConnectionManager) GetAddrManager() *addrmgr.AddrManager {
	return cmgr.AddrMgr
}

func (cmgr *ConnectionManager) SetTargetOutboundPeers(numPeers uint32) {
	cmgr.targetOutboundPeers = numPeers
}

// Check if the address passed shares a group with any addresses already in our
// data structures.
func (cmgr *ConnectionManager) IsFromRedundantOutboundIPAddress(na *wire.NetAddress) bool {
	groupKey := addrmgr.GroupKey(na)

	cmgr.mtxOutboundConnIPGroups.Lock()
	numGroupsForKey := cmgr.outboundConnIPGroups[groupKey]
	cmgr.mtxOutboundConnIPGroups.Unlock()

	if numGroupsForKey != 0 && numGroupsForKey != 1 {
		glog.V(2).Infof("IsFromRedundantOutboundIPAddress: Found numGroupsForKey != (0 or 1). Is (%d) "+
			"instead for addr (%s) and group key (%s). This "+
			"should never happen.", numGroupsForKey, na.IP.String(), groupKey)
	}

	if numGroupsForKey == 0 {
		return false
	}
	return true
}

func (cmgr *ConnectionManager) addToGroupKey(na *wire.NetAddress) {
	groupKey := addrmgr.GroupKey(na)

	cmgr.mtxOutboundConnIPGroups.Lock()
	cmgr.outboundConnIPGroups[groupKey]++
	cmgr.mtxOutboundConnIPGroups.Unlock()
}

func (cmgr *ConnectionManager) subFromGroupKey(na *wire.NetAddress) {
	groupKey := addrmgr.GroupKey(na)

	cmgr.mtxOutboundConnIPGroups.Lock()
	cmgr.outboundConnIPGroups[groupKey]--
	cmgr.mtxOutboundConnIPGroups.Unlock()
}

func (cmgr *ConnectionManager) getRandomAddr() *wire.NetAddress {
	for tries := 0; tries < 100; tries++ {
		addr := cmgr.AddrMgr.GetAddress()
		if addr == nil {
			glog.V(2).Infof("ConnectionManager.getRandomAddr: addr from GetAddressWithExclusions was nil")
			break
		}

		// Lock the address map since multiple threads will be trying to read
		// and modify it at the same time.
		cmgr.mtxConnectedOutboundAddrs.RLock()
		ok := cmgr.connectedOutboundAddrs[addrmgr.NetAddressKey(addr.NetAddress())]
		cmgr.mtxConnectedOutboundAddrs.RUnlock()
		if ok {
			glog.V(2).Infof("ConnectionManager.getRandomAddr: Not choosing already connected address %v:%v", addr.NetAddress().IP, addr.NetAddress().Port)
			continue
		}

		// We can only have one outbound address per /16. This is similar to
		// Bitcoin and we do it to prevent Sybil attacks.
		if cmgr.IsFromRedundantOutboundIPAddress(addr.NetAddress()) {
			glog.V(2).Infof("ConnectionManager.getRandomAddr: Not choosing address due to redundant group key %v:%v", addr.NetAddress().IP, addr.NetAddress().Port)
			continue
		}

		glog.V(2).Infof("ConnectionManager.getRandomAddr: Returning %v:%v at %d iterations",
			addr.NetAddress().IP, addr.NetAddress().Port, tries)
		return addr.NetAddress()
	}

	glog.V(2).Infof("ConnectionManager.getRandomAddr: Returning nil")
	return nil
}

func _delayRetry(retryCount uint64, persistentAddrForLogging *wire.NetAddress, unit time.Duration) (_retryDuration time.Duration) {
	// No delay if we haven't tried yet or if the number of retries isn't positive.
	if retryCount <= 0 {
		return 0
	}
	numSecs := int(math.Pow(2.0, float64(retryCount)))
	retryDelay := time.Duration(numSecs) * unit

	if persistentAddrForLogging != nil {
		glog.V(1).Infof("Retrying connection to outbound persistent peer: "+
			"(%s:%d) in (%d) seconds.", persistentAddrForLogging.IP.String(),
			persistentAddrForLogging.Port, numSecs)
	} else {
		glog.V(2).Infof("Retrying connection to outbound non-persistent peer in (%d) seconds.", numSecs)
	}
	return retryDelay
}

func (cmgr *ConnectionManager) enoughOutboundPeers() bool {
	val := atomic.LoadUint32(&cmgr.numOutboundPeers)
	if val > cmgr.targetOutboundPeers {
		glog.Errorf("enoughOutboundPeers: Connected to too many outbound "+
			"peers: (%d). Should be "+
			"no more than (%d).", val, cmgr.targetOutboundPeers)
		return true
	}

	if val == cmgr.targetOutboundPeers {
		return true
	}
	return false
}

func IPToNetAddr(ipStr string, addrMgr *addrmgr.AddrManager, params *DeSoParams) (*wire.NetAddress, error) {
	port := params.DefaultSocketPort
	host, portstr, err := net.SplitHostPort(ipStr)
	if err != nil {
		// No port specified so leave port=default and set
		// host to the ipStr.
		host = ipStr
	} else {
		pp, err := strconv.ParseUint(portstr, 10, 16)
		if err != nil {
			return nil, errors.Wrapf(err, "IPToNetAddr: Can not parse port from %s for ip", ipStr)
		}
		port = uint16(pp)
	}
	netAddr, err := addrMgr.HostToNetAddress(host, port, 0)
	if err != nil {
		return nil, errors.Wrapf(err, "IPToNetAddr: Can not parse port from %s for ip", ipStr)
	}
	return netAddr, nil
}

func (cmgr *ConnectionManager) IsConnectedOutboundIpAddress(netAddr *wire.NetAddress) bool {
	// Lock the address map since multiple threads will be trying to read
	// and modify it at the same time.
	cmgr.mtxConnectedOutboundAddrs.RLock()
	defer cmgr.mtxConnectedOutboundAddrs.RUnlock()
	return cmgr.connectedOutboundAddrs[addrmgr.NetAddressKey(netAddr)]
}

func (cmgr *ConnectionManager) IsAttemptedOutboundIpAddress(netAddr *wire.NetAddress) bool {
	return cmgr.attemptedOutboundAddrs[addrmgr.NetAddressKey(netAddr)]
}

func (cmgr *ConnectionManager) AddAttemptedOutboundAddrs(netAddr *wire.NetAddress) {
	cmgr.attemptedOutboundAddrs[addrmgr.NetAddressKey(netAddr)] = true
}

func (cmgr *ConnectionManager) RemoveAttemptedOutboundAddrs(netAddr *wire.NetAddress) {
	delete(cmgr.attemptedOutboundAddrs, addrmgr.NetAddressKey(netAddr))
}

// DialPersistentOutboundConnection attempts to connect to a persistent peer.
func (cmgr *ConnectionManager) DialPersistentOutboundConnection(persistentAddr *wire.NetAddress) (_attemptId uint64) {
	glog.V(2).Infof("ConnectionManager.DialPersistentOutboundConnection: Connecting to peer %v", persistentAddr.IP.String())
	return cmgr._dialOutboundConnection(persistentAddr, true)
}

// DialOutboundConnection attempts to connect to a non-persistent peer.
func (cmgr *ConnectionManager) DialOutboundConnection(addr *wire.NetAddress) (_attemptId uint64) {
	glog.V(2).Infof("ConnectionManager.ConnectOutboundConnection: Connecting to peer %v", addr.IP.String())
	return cmgr._dialOutboundConnection(addr, false)
}

// CloseAttemptedConnection closes an ongoing connection attempt.
func (cmgr *ConnectionManager) CloseAttemptedConnection(attemptId uint64) {
	glog.V(2).Infof("ConnectionManager.CloseAttemptedConnection: Closing connection attempt %d", attemptId)
	if attempt, exists := cmgr.outboundConnectionAttempts[attemptId]; exists {
		attempt.Stop()
	}
}

// _dialOutboundConnection is the internal method that spawns and initiates an OutboundConnectionAttempt, which handles the
// connection attempt logic. It returns the attemptId of the attempt that was created.
func (cmgr *ConnectionManager) _dialOutboundConnection(addr *wire.NetAddress, isPersistent bool) (_attemptId uint64) {
	attemptId := atomic.AddUint64(&cmgr.attemptIndex, 1)
	connectionAttempt := NewOutboundConnectionAttempt(attemptId, addr, isPersistent,
		cmgr.params.DialTimeout, cmgr.outboundConnectionChan)
	cmgr.outboundConnectionAttempts[connectionAttempt.attemptId] = connectionAttempt
	cmgr.AddAttemptedOutboundAddrs(addr)

	connectionAttempt.Start()
	return attemptId
}

// ConnectPeer connects either an INBOUND or OUTBOUND peer. If Conn == nil,
// then we will set up an OUTBOUND peer. Otherwise we will use the Conn to
// create an INBOUND peer. If the connection is OUTBOUND and the persistentAddr
// is set, then we will connect only to that addr. Otherwise, we will use
// the addrmgr to randomly select addrs and create OUTBOUND connections
// with them until we find a worthy peer.
func (cmgr *ConnectionManager) ConnectPeer(conn net.Conn, na *wire.NetAddress, attemptId uint64, isOutbound bool, isPersistent bool) *Peer {
	// At this point Conn is set so create a peer object to do a version negotiation.
	id := atomic.AddUint64(&cmgr.peerIndex, 1)
	peer := NewPeer(id, attemptId, conn, isOutbound, na, isPersistent,
		cmgr.stallTimeoutSeconds,
		cmgr.minFeeRateNanosPerKB,
		cmgr.params,
		cmgr.srv.incomingMessages, cmgr, cmgr.srv, cmgr.SyncType,
		cmgr.newPeerChan, cmgr.donePeerChan)

	// Now we can add the peer to our data structures.
	peer._logAddPeer()
	cmgr.addPeer(peer)

	// Start the peer's message loop.
	peer.Start()

	// FIXME: Move this earlier
	// Signal the server about the new Peer in case it wants to do something with it.
	go func() {
		cmgr.serverMessageQueue <- &ServerMessage{
			Peer: peer,
			Msg:  &MsgDeSoNewPeer{},
		}
	}()

	return peer
}

func (cmgr *ConnectionManager) _isFromRedundantInboundIPAddress(addrToCheck net.Addr) bool {
	cmgr.mtxPeerMaps.RLock()
	defer cmgr.mtxPeerMaps.RUnlock()

	// Loop through all the peers to see if any have the same IP
	// address. This map is normally pretty small so doing this
	// every time a Peer connects should be fine.
	netAddr, err := IPToNetAddr(addrToCheck.String(), cmgr.AddrMgr, cmgr.params)
	if err != nil {
		// Return true in case we have an error. We do this because it
		// will result in the peer connection not being accepted, which
		// is desired in this case.
		glog.Warningf(errors.Wrapf(err,
			"ConnectionManager._isFromRedundantInboundIPAddress: Problem parsing "+
				"net.Addr to wire.NetAddress so marking as redundant and not "+
				"making connection").Error())
		return true
	}
	if netAddr == nil {
		glog.Warningf("ConnectionManager._isFromRedundantInboundIPAddress: " +
			"address was nil after parsing so marking as redundant and not " +
			"making connection")
		return true
	}
	// If the IP is a localhost IP let it slide. This is useful for testing fake
	// nodes on a local machine.
	// TODO: Should this be a flag?
	if net.IP([]byte{127, 0, 0, 1}).Equal(netAddr.IP) {
		glog.V(1).Infof("ConnectionManager._isFromRedundantInboundIPAddress: Allowing " +
			"localhost IP address to connect")
		return false
	}
	for _, peer := range cmgr.inboundPeers {
		// If the peer's IP is equal to the passed IP then we have found a duplicate
		// inbound connection
		if peer.netAddr.IP.Equal(netAddr.IP) {
			return true
		}
	}

	// If we get here then no duplicate inbound IPs were found.
	return false
}

func (cmgr *ConnectionManager) _handleInboundConnections() {
	for _, outerListener := range cmgr.listeners {
		go func(ll net.Listener) {
			for {
				conn, err := ll.Accept()
				if conn == nil {
					return
				}
				glog.V(2).Infof("_handleInboundConnections: received connection from: local %v, remote %v",
					conn.LocalAddr().String(), conn.RemoteAddr().String())
				if atomic.LoadInt32(&cmgr.shutdown) != 0 {
					glog.Info("_handleInboundConnections: Ignoring connection due to shutdown")
					return
				}
				if err != nil {
					glog.Errorf("_handleInboundConnections: Can't accept connection: %v", err)
					continue
				}

				// As a quick check, reject the peer if we have too many already. Note that
				// this check isn't perfect but we have a later check at the end after doing
				// a version negotiation that will properly reject the peer if this check
				// messes up e.g. due to a concurrency issue.
				//
				// TODO: We should instead have eviction logic here to prevent
				// someone from monopolizing a node's inbound connections.
				numInboundPeers := atomic.LoadUint32(&cmgr.numInboundPeers)
				if numInboundPeers > cmgr.maxInboundPeers {

					glog.Infof("Rejecting INBOUND peer (%s) due to max inbound peers (%d) hit.",
						conn.RemoteAddr().String(), cmgr.maxInboundPeers)
					conn.Close()

					continue
				}

				// If we want to limit inbound connections to one per IP address, check to
				// make sure this address isn't already connected.
				if cmgr.limitOneInboundConnectionPerIP &&
					cmgr._isFromRedundantInboundIPAddress(conn.RemoteAddr()) {

					glog.Infof("Rejecting INBOUND peer (%s) due to already having an "+
						"inbound connection from the same IP with "+
						"limit_one_inbound_connection_per_ip set.",
						conn.RemoteAddr().String())
					conn.Close()

					continue
				}

				cmgr.inboundConnectionChan <- &inboundConnection{
					connection: conn,
				}
			}
		}(outerListener)
	}
}

// GetAllPeers holds the mtxPeerMaps lock for reading and returns a list containing
// pointers to all the active peers.
func (cmgr *ConnectionManager) GetAllPeers() []*Peer {
	cmgr.mtxPeerMaps.RLock()
	defer cmgr.mtxPeerMaps.RUnlock()

	allPeers := []*Peer{}
	for _, pp := range cmgr.persistentPeers {
		allPeers = append(allPeers, pp)
	}
	for _, pp := range cmgr.outboundPeers {
		allPeers = append(allPeers, pp)
	}
	for _, pp := range cmgr.inboundPeers {
		allPeers = append(allPeers, pp)
	}

	return allPeers
}

func (cmgr *ConnectionManager) RandomPeer() *Peer {
	cmgr.mtxPeerMaps.RLock()
	defer cmgr.mtxPeerMaps.RUnlock()

	// Prefer persistent peers over all other peers.
	if len(cmgr.persistentPeers) > 0 {
		// Maps iterate randomly so this should be sufficient.
		for _, pp := range cmgr.persistentPeers {
			return pp
		}
	}

	// Prefer outbound peers over inbound peers.
	if len(cmgr.outboundPeers) > 0 {
		// Maps iterate randomly so this should be sufficient.
		for _, pp := range cmgr.outboundPeers {
			return pp
		}
	}

	// If we don't have any other type of peer, use an inbound peer.
	if len(cmgr.inboundPeers) > 0 {
		// Maps iterate randomly so this should be sufficient.
		for _, pp := range cmgr.inboundPeers {
			return pp
		}
	}

	return nil
}

// Update our data structures to add this peer.
func (cmgr *ConnectionManager) addPeer(pp *Peer) {
	// Acquire the mtxPeerMaps lock for writing.
	cmgr.mtxPeerMaps.Lock()
	defer cmgr.mtxPeerMaps.Unlock()

	// Figure out what list this peer belongs to.
	var peerList map[uint64]*Peer
	if pp.isPersistent {
		peerList = cmgr.persistentPeers
		atomic.AddUint32(&cmgr.numPersistentPeers, 1)
	} else if pp.isOutbound {
		peerList = cmgr.outboundPeers

		// If this is a non-persistent outbound peer and if
		// the peer was not previously in our data structures then
		// increment the count for this IP group and increment the
		// number of outbound peers. Also add the peer's address to
		// our map.
		if _, ok := peerList[pp.ID]; !ok {
			cmgr.addToGroupKey(pp.netAddr)
			atomic.AddUint32(&cmgr.numOutboundPeers, 1)

			cmgr.mtxConnectedOutboundAddrs.Lock()
			cmgr.connectedOutboundAddrs[addrmgr.NetAddressKey(pp.netAddr)] = true
			cmgr.mtxConnectedOutboundAddrs.Unlock()
		}
	} else {
		// This is an inbound peer.
		atomic.AddUint32(&cmgr.numInboundPeers, 1)
		peerList = cmgr.inboundPeers
	}

	peerList[pp.ID] = pp
	cmgr.connectedPeers[pp.ID] = pp
}

func (cmgr *ConnectionManager) SendMessage(msg DeSoMessage, peerId uint64) error {
	if peer, ok := cmgr.connectedPeers[peerId]; ok {
		glog.V(1).Infof("SendMessage: Sending message %v to peer %d", msg.GetMsgType().String(), peerId)
		peer.AddDeSoMessage(msg, false)
	} else {
		return fmt.Errorf("SendMessage: Peer with ID %d not found", peerId)
	}
	return nil
}

func (cmgr *ConnectionManager) CloseConnection(peerId uint64) {
	glog.V(2).Infof("ConnectionManager.CloseConnection: Closing connection to peer (id= %v)", peerId)

	var peer *Peer
	var ok bool
	cmgr.mtxPeerMaps.Lock()
	peer, ok = cmgr.connectedPeers[peerId]
	cmgr.mtxPeerMaps.Unlock()
	if !ok {
		return
	}
	peer.Disconnect()
}

// Update our data structures to remove this peer.
func (cmgr *ConnectionManager) removePeer(pp *Peer) {
	// Acquire the mtxPeerMaps lock for writing.
	cmgr.mtxPeerMaps.Lock()
	defer cmgr.mtxPeerMaps.Unlock()

	// Figure out what list this peer belongs to.
	var peerList map[uint64]*Peer
	if pp.isPersistent {
		peerList = cmgr.persistentPeers
		atomic.AddUint32(&cmgr.numPersistentPeers, Uint32Dec)
	} else if pp.isOutbound {
		peerList = cmgr.outboundPeers

		// If this is a non-persistent outbound peer and if
		// the peer was previously in our data structures then
		// decrement the outbound group count and the number of
		// outbound peers.
		if _, ok := peerList[pp.ID]; ok {
			cmgr.subFromGroupKey(pp.netAddr)
			atomic.AddUint32(&cmgr.numOutboundPeers, Uint32Dec)

			cmgr.mtxConnectedOutboundAddrs.Lock()
			delete(cmgr.connectedOutboundAddrs, addrmgr.NetAddressKey(pp.netAddr))
			cmgr.mtxConnectedOutboundAddrs.Unlock()
		}
	} else {
		// This is an inbound peer.
		atomic.AddUint32(&cmgr.numInboundPeers, Uint32Dec)
		peerList = cmgr.inboundPeers
	}

	// Update the last seen time before we finish removing the peer.
	// TODO: Really, we call 'Connected()' on removing a peer?
	// I can't find a Disconnected() but seems odd.
	cmgr.AddrMgr.Connected(pp.netAddr)

	// Remove the peer from our data structure.
	delete(peerList, pp.ID)
	delete(cmgr.connectedPeers, pp.ID)
}

func (cmgr *ConnectionManager) _maybeReplacePeer(pp *Peer) {
	// If the peer was outbound, replace her with a
	// new peer to maintain a fixed number of outbound connections.
	if pp.isOutbound {
		// If the peer is not persistent then we don't want to pass an
		// address to connectPeer. The lack of an address will cause it
		// to choose random addresses from the addrmgr until one works.
		na := pp.netAddr
		if !pp.isPersistent {
			na = nil
		}
		cmgr._dialOutboundConnection(na, pp.isPersistent)
	}
}

func (cmgr *ConnectionManager) _logOutboundPeerData() {
	numOutboundPeers := int(atomic.LoadUint32(&cmgr.numOutboundPeers))
	numInboundPeers := int(atomic.LoadUint32(&cmgr.numInboundPeers))
	numPersistentPeers := int(atomic.LoadUint32(&cmgr.numPersistentPeers))
	glog.V(1).Infof("Num peers: OUTBOUND(%d) INBOUND(%d) PERSISTENT(%d)", numOutboundPeers, numInboundPeers, numPersistentPeers)

	cmgr.mtxOutboundConnIPGroups.Lock()
	for _, vv := range cmgr.outboundConnIPGroups {
		if vv != 0 && vv != 1 {
			glog.V(1).Infof("_logOutboundPeerData: Peer group count != (0 or 1). "+
				"Is (%d) instead. This "+
				"should never happen.", vv)
		}
	}
	cmgr.mtxOutboundConnIPGroups.Unlock()
}

func (cmgr *ConnectionManager) AddTimeSample(addrStr string, timeSample time.Time) {
	cmgr.timeSource.AddTimeSample(addrStr, timeSample)
}

func (cmgr *ConnectionManager) GetNumInboundPeers() uint32 {
	return atomic.LoadUint32(&cmgr.numInboundPeers)
}

func (cmgr *ConnectionManager) GetNumOutboundPeers() uint32 {
	return atomic.LoadUint32(&cmgr.numOutboundPeers)
}

func (cmgr *ConnectionManager) Stop() {
	if atomic.AddInt32(&cmgr.shutdown, 1) != 1 {
		glog.Warningf("ConnectionManager.Stop is already in the process of " +
			"shutting down")
		return
	}
	for _, ca := range cmgr.outboundConnectionAttempts {
		ca.Stop()
	}
	glog.Infof("ConnectionManager: Stopping, number of inbound peers (%v), number of outbound "+
		"peers (%v), number of persistent peers (%v).", len(cmgr.inboundPeers), len(cmgr.outboundPeers),
		len(cmgr.persistentPeers))
	for _, peer := range cmgr.inboundPeers {
		glog.V(1).Infof(CLog(Red, fmt.Sprintf("ConnectionManager.Stop: Inbound peer (%v)", peer)))
		peer.Disconnect()
	}
	for _, peer := range cmgr.outboundPeers {
		glog.V(1).Infof("ConnectionManager.Stop: Outbound peer (%v)", peer)
		peer.Disconnect()
	}
	for _, peer := range cmgr.persistentPeers {
		glog.V(1).Infof("ConnectionManager.Stop: Persistent peer (%v)", peer)
		peer.Disconnect()
	}

	// Close all of the listeners.
	for _, listener := range cmgr.listeners {
		_ = listener.Close()
	}
}

func (cmgr *ConnectionManager) Start() {
	// Below is a basic description of the ConnectionManager's main loop:
	//
	// We have listeners (for inbound connections) and we have an addrmgr (for outbound connections).
	// Specify TargetOutbound connections we want to have.
	// Create TargetOutbound connection objects each with their own id.
	// Add these connection objects to a map of some sort.
	// Initiate TargetOutbound connections to peers using the addrmgr.
	// When a connection fails, remove that connection from the map and try another connection in its place. Wait for that connection to return. Repeat.
	// - If a connection has failed a few times then add a retryduration (since we're probably out of addresses).
	// - If you can't connect to a node because the addrmgr returned nil, wait some amount of time and then try again.
	// When a connection succeeds:
	// - Send the peer a version message.
	// - Read a version message from the peer.
	// - Wait for the above two steps to return.
	// - If the above steps don't return, then disconnect from the peer as above. Try to reconnect to another peer.
	// If the steps above succeed
	// - Have the peer enter a switch statement listening for all kinds of messages.
	// - Send addr and getaddr messages as appropriate.

	// Accept inbound connections from peers on our listeners.
	cmgr._handleInboundConnections()

	glog.Infof("Full node socket initialized")

	for {
		// Log some data for each event.
		cmgr._logOutboundPeerData()

		select {
		case oc := <-cmgr.outboundConnectionChan:
			glog.V(2).Infof("ConnectionManager.Start: Successfully established an outbound connection with "+
				"(addr= %v)", oc.connection.RemoteAddr())
			cmgr.serverMessageQueue <- &ServerMessage{
				Peer: nil,
				Msg: &MsgDeSoNewConnection{
					Connection: oc,
				},
			}
		case ic := <-cmgr.inboundConnectionChan:
			glog.V(2).Infof("ConnectionManager.Start: Successfully received an inbound connection from "+
				"(addr= %v)", ic.connection.RemoteAddr())
			cmgr.serverMessageQueue <- &ServerMessage{
				Peer: nil,
				Msg: &MsgDeSoNewConnection{
					Connection: ic,
				},
			}
		case pp := <-cmgr.newPeerChan:
			{
				// We have successfully connected to a peer and it passed its version
				// negotiation.

				// if this is a non-persistent outbound peer and we already have enough
				// outbound peers, then don't bother adding this one.
				if !pp.isPersistent && pp.isOutbound && cmgr.enoughOutboundPeers() {
					// TODO: Make this less verbose
					glog.V(1).Infof("Dropping peer because we already have enough outbound peer connections.")
					pp.Conn.Close()
					continue
				}

				// If this is a non-persistent outbound peer and the group key
				// overlaps with another peer we're already connected to then
				// abort mission. We only connect to one peer per IP group in
				// order to prevent Sybil attacks.
				if pp.isOutbound &&
					!pp.isPersistent &&
					cmgr.IsFromRedundantOutboundIPAddress(pp.netAddr) {

					// TODO: Make this less verbose
					glog.Infof("Rejecting OUTBOUND NON-PERSISTENT peer (%v) with "+
						"redundant group key (%s).",
						pp, addrmgr.GroupKey(pp.netAddr))

					pp.Conn.Close()
					cmgr._maybeReplacePeer(pp)
					continue
				}

				// Check that we have not exceeded the maximum number of inbound
				// peers allowed.
				//
				// TODO: We should instead have eviction logic to prevent
				// someone from monopolizing a node's inbound connections.
				numInboundPeers := atomic.LoadUint32(&cmgr.numInboundPeers)
				if !pp.isOutbound && numInboundPeers > cmgr.maxInboundPeers {

					// TODO: Make this less verbose
					glog.Infof("Rejecting INBOUND peer (%v) due to max inbound peers (%d) hit.",
						pp, cmgr.maxInboundPeers)

					pp.Conn.Close()
					continue
				}

				// Now we can add the peer to our data structures.
				pp._logAddPeer()
				cmgr.addPeer(pp)

				// Start the peer's message loop.
				pp.Start()

				// Signal the server about the new Peer in case it wants to do something with it.
				cmgr.serverMessageQueue <- &ServerMessage{
					Peer: pp,
					Msg:  &MsgDeSoNewPeer{},
				}

			}
		case pp := <-cmgr.donePeerChan:
			{
				// By the time we get here, it can be assumed that the Peer's Disconnect function
				// has already been called, since that is what's responsible for adding the peer
				// to this queue in the first place.

				glog.V(1).Infof("Done with peer (%v).", pp)

				// Remove the peer from our data structures.
				cmgr.removePeer(pp)

				// Potentially replace the peer. For example, if the Peer was an outbound Peer
				// then we want to find a new peer in order to maintain our TargetOutboundPeers.
				cmgr._maybeReplacePeer(pp)

				// Signal the server about the Peer being done in case it wants to do something
				// with it.
				cmgr.serverMessageQueue <- &ServerMessage{
					Peer: pp,
					Msg:  &MsgDeSoDonePeer{},
				}
			}
		}
	}
}
