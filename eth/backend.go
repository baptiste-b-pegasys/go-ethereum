// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package eth implements the Ethereum protocol.
package eth

import (
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/clique"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/consensus/istanbul"
	istanbulBackend "github.com/ethereum/go-ethereum/consensus/istanbul/backend"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/bloombits"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/eth/filters"
	"github.com/ethereum/go-ethereum/eth/gasprice"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/miner"
	"github.com/ethereum/go-ethereum/multitenancy"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/p2p/enr"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
)

// Ethereum implements the Ethereum full node service.
type Ethereum struct {
	config *Config

	// Handlers
	txPool          *core.TxPool
	blockchain      *core.BlockChain
	protocolManager *ProtocolManager
	dialCandidates  enode.Iterator

	// DB interfaces
	chainDb ethdb.Database // Block chain database

	eventMux       *event.TypeMux
	engine         consensus.Engine
	accountManager *accounts.Manager

	bloomRequests     chan chan *bloombits.Retrieval // Channel receiving bloom data retrieval requests
	bloomIndexer      *core.ChainIndexer             // Bloom indexer operating during block imports
	closeBloomHandler chan struct{}

	APIBackend *EthAPIBackend

	miner     *miner.Miner
	gasPrice  *big.Int
	etherbase common.Address

	networkID     uint64
	netRPCService *ethapi.PublicNetAPI

	p2pServer *p2p.Server

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and etherbase)

	// Quorum - Multitenancy
	// contractAuthzProvider is set after node starts instead in New()
	contractAuthzProvider multitenancy.ContractAuthorizationProvider

	// Quorum - consensus as eth-service (e.g. raft)
	consensusServicePendingLogsFeed *event.Feed
}

// Quorum
//
// Set the decision manager for multitenancy support
func (s *Ethereum) SetContractAuthorizationProvider(dm multitenancy.ContractAuthorizationProvider) {
	s.contractAuthzProvider = dm
}

// New creates a new Ethereum object (including the
// initialisation of the common Ethereum object)
func New(stack *node.Node, config *Config) (*Ethereum, error) {
	// Ensure configuration values are compatible and sane
	if config.SyncMode == downloader.LightSync {
		return nil, errors.New("can't run eth.Ethereum in light sync mode, use les.LightEthereum")
	}
	if !config.SyncMode.IsValid() {
		return nil, fmt.Errorf("invalid sync mode %d", config.SyncMode)
	}
	if config.Miner.GasPrice == nil || config.Miner.GasPrice.Cmp(common.Big0) <= 0 {
		log.Warn("Sanitizing invalid miner gas price", "provided", config.Miner.GasPrice, "updated", DefaultConfig.Miner.GasPrice)
		config.Miner.GasPrice = new(big.Int).Set(DefaultConfig.Miner.GasPrice)
	}
	if config.NoPruning && config.TrieDirtyCache > 0 {
		if config.SnapshotCache > 0 {
			config.TrieCleanCache += config.TrieDirtyCache * 3 / 5
			config.SnapshotCache += config.TrieDirtyCache * 2 / 5
		} else {
			config.TrieCleanCache += config.TrieDirtyCache
		}
		config.TrieDirtyCache = 0
	}
	log.Info("Allocated trie memory caches", "clean", common.StorageSize(config.TrieCleanCache)*1024*1024, "dirty", common.StorageSize(config.TrieDirtyCache)*1024*1024)

	// Assemble the Ethereum object
	chainDb, err := stack.OpenDatabaseWithFreezer("chaindata", config.DatabaseCache, config.DatabaseHandles, config.DatabaseFreezer, "eth/db/chaindata/")
	if err != nil {
		return nil, err
	}
	chainConfig, genesisHash, genesisErr := core.SetupGenesisBlock(chainDb, config.Genesis)
	if _, ok := genesisErr.(*params.ConfigCompatError); genesisErr != nil && !ok {
		return nil, genesisErr
	}
	log.Info("Initialised chain configuration", "config", chainConfig)

	if chainConfig.IsQuorum {
		// changes to manipulate the chain id for migration from 2.0.2 and below version to 2.0.3
		// version of Quorum  - this is applicable for v2.0.3 onwards
		if (chainConfig.ChainID != nil && chainConfig.ChainID.Int64() == 1) || config.NetworkId == 1 {
			return nil, errors.New("Cannot have chain id or network id as 1.")
		}

		if config.QuorumPrivacyMarkerTransactionsEnabled && chainConfig.QuorumPrecompilesV1Block == nil {
			return nil, errors.New("Privacy marker transactions require quorumPrecompilesV1Block to be set in genesis.json")
		}
	}

	if !rawdb.GetIsQuorumEIP155Activated(chainDb) && chainConfig.ChainID != nil {
		//Upon starting the node, write the flag to disallow changing ChainID/EIP155 block after HF
		rawdb.WriteQuorumEIP155Activation(chainDb)
	}

	eth := &Ethereum{
		config:                          config,
		chainDb:                         chainDb,
		eventMux:                        stack.EventMux(),
		accountManager:                  stack.AccountManager(),
		engine:                          CreateConsensusEngine(stack, chainConfig, config, config.Miner.Notify, config.Miner.Noverify, chainDb),
		closeBloomHandler:               make(chan struct{}),
		networkID:                       config.NetworkId,
		gasPrice:                        config.Miner.GasPrice,
		etherbase:                       config.Miner.Etherbase,
		bloomRequests:                   make(chan chan *bloombits.Retrieval),
		bloomIndexer:                    NewBloomIndexer(chainDb, params.BloomBitsBlocks, params.BloomConfirms),
		p2pServer:                       stack.Server(),
		consensusServicePendingLogsFeed: new(event.Feed),
	}

	// Quorum: Set protocol Name/Version
	// keep `var protocolName = "eth"` as is, and only update the quorum consensus specific protocol
	// This is used to enable the eth service to return multiple devp2p subprotocols.
	// Previously, for istanbul/64 istnbul/99 and clique (v2.6) `protocolName` would be overridden and
	// set to the consensus subprotocol name instead of "eth", meaning the node would no longer
	// communicate over the "eth" subprotocol, e.g. "eth" or "istanbul/99" but not eth" and "istanbul/99".
	// With this change, support is added so that the "eth" subprotocol remains and optionally a consensus subprotocol
	// can be added allowing the node to communicate over "eth" and an optional consensus subprotocol, e.g. "eth" and "istanbul/100"
	if chainConfig.IsQuorum {
		quorumProtocol := eth.engine.Protocol()
		// set the quorum specific consensus devp2p subprotocol, eth subprotocol remains set to protocolName as in upstream geth.
		quorumConsensusProtocolName = quorumProtocol.Name
		quorumConsensusProtocolVersions = quorumProtocol.Versions
		quorumConsensusProtocolLengths = quorumProtocol.Lengths
	}

	// force to set the istanbul etherbase to node key address
	if chainConfig.Istanbul != nil {
		eth.etherbase = crypto.PubkeyToAddress(stack.GetNodeKey().PublicKey)
	}
	bcVersion := rawdb.ReadDatabaseVersion(chainDb)
	var dbVer = "<nil>"
	if bcVersion != nil {
		dbVer = fmt.Sprintf("%d", *bcVersion)
	}
	log.Info("Initialising Ethereum protocol", "name", protocolName, "versions", ProtocolVersions, "network", config.NetworkId, "dbversion", dbVer)
	if chainConfig.IsQuorum {
		log.Info("Initialising Quorum consensus protocol", "name", quorumConsensusProtocolName, "versions", quorumConsensusProtocolVersions, "network", config.NetworkId, "dbversion", dbVer)
	}

	if !config.SkipBcVersionCheck {
		if bcVersion != nil && *bcVersion > core.BlockChainVersion {
			return nil, fmt.Errorf("database version is v%d, Geth %s only supports v%d", *bcVersion, params.VersionWithMeta, core.BlockChainVersion)
		} else if bcVersion == nil || *bcVersion < core.BlockChainVersion {
			log.Warn("Upgrade blockchain database version", "from", dbVer, "to", core.BlockChainVersion)
			rawdb.WriteDatabaseVersion(chainDb, core.BlockChainVersion)
		}
	}
	var (
		vmConfig = vm.Config{
			EnablePreimageRecording: config.EnablePreimageRecording,
			EWASMInterpreter:        config.EWASMInterpreter,
			EVMInterpreter:          config.EVMInterpreter,
		}
		cacheConfig = &core.CacheConfig{
			TrieCleanLimit:      config.TrieCleanCache,
			TrieCleanJournal:    stack.ResolvePath(config.TrieCleanCacheJournal),
			TrieCleanRejournal:  config.TrieCleanCacheRejournal,
			TrieCleanNoPrefetch: config.NoPrefetch,
			TrieDirtyLimit:      config.TrieDirtyCache,
			TrieDirtyDisabled:   config.NoPruning,
			TrieTimeLimit:       config.TrieTimeout,
			SnapshotLimit:       config.SnapshotCache,
		}
	)
	newBlockChainFunc := core.NewBlockChain
	if config.EnableMultitenancy {
		newBlockChainFunc = core.NewMultitenantBlockChain
	}
	eth.blockchain, err = newBlockChainFunc(chainDb, cacheConfig, chainConfig, eth.engine, vmConfig, eth.shouldPreserve, &config.TxLookupLimit)
	if err != nil {
		return nil, err
	}
	// Rewind the chain in case of an incompatible config upgrade.
	if compat, ok := genesisErr.(*params.ConfigCompatError); ok {
		log.Warn("Rewinding chain to upgrade configuration", "err", compat)
		eth.blockchain.SetHead(compat.RewindTo)
		rawdb.WriteChainConfig(chainDb, genesisHash, chainConfig)
	}
	eth.bloomIndexer.Start(eth.blockchain)

	if config.TxPool.Journal != "" {
		config.TxPool.Journal = stack.ResolvePath(config.TxPool.Journal)
	}
	eth.txPool = core.NewTxPool(config.TxPool, chainConfig, eth.blockchain)

	// Permit the downloader to use the trie cache allowance during fast sync
	cacheLimit := cacheConfig.TrieCleanLimit + cacheConfig.TrieDirtyLimit + cacheConfig.SnapshotLimit
	checkpoint := config.Checkpoint
	if checkpoint == nil {
		checkpoint = params.TrustedCheckpoints[genesisHash]
	}
	if eth.protocolManager, err = NewProtocolManager(chainConfig, checkpoint, config.SyncMode, config.NetworkId, eth.eventMux, eth.txPool, eth.engine, eth.blockchain, chainDb, cacheLimit, config.Whitelist, config.RaftMode); err != nil {
		return nil, err
	}
	eth.miner = miner.New(eth, &config.Miner, chainConfig, eth.EventMux(), eth.engine, eth.isLocalBlock)
	eth.miner.SetExtra(makeExtraData(config.Miner.ExtraData, eth.blockchain.Config().IsQuorum))

	hexNodeId := fmt.Sprintf("%x", crypto.FromECDSAPub(&stack.GetNodeKey().PublicKey)[1:]) // Quorum
	eth.APIBackend = &EthAPIBackend{stack.Config().ExtRPCEnabled(), eth, nil, hexNodeId, config.EVMCallTimeOut}

	// (Quorum) enable the txpool to determine when it needs to apply PMT nonce corrections
	eth.txPool.SetQuorumCreatePrivacyMarkerTransactions(eth.APIBackend.QuorumCreatePrivacyMarkerTransactions)

	gpoParams := config.GPO
	if gpoParams.Default == nil {
		gpoParams.Default = config.Miner.GasPrice
	}
	eth.APIBackend.gpo = gasprice.NewOracle(eth.APIBackend, gpoParams)

	eth.dialCandidates, err = eth.setupDiscovery(&stack.Config().P2P)
	if err != nil {
		return nil, err
	}

	// Start the RPC service
	eth.netRPCService = ethapi.NewPublicNetAPI(eth.p2pServer, eth.NetVersion())

	// Register the backend on the node
	stack.RegisterAPIs(eth.APIs())
	stack.RegisterProtocols(eth.Protocols())
	stack.RegisterLifecycle(eth)
	return eth, nil
}

func makeExtraData(extra []byte, isQuorum bool) []byte {
	if len(extra) == 0 {
		// create default extradata
		extra, _ = rlp.EncodeToBytes([]interface{}{
			uint(params.VersionMajor<<16 | params.VersionMinor<<8 | params.VersionPatch),
			"geth",
			runtime.Version(),
			runtime.GOOS,
		})
	}
	if uint64(len(extra)) > params.GetMaximumExtraDataSize(isQuorum) {
		log.Warn("Miner extra data exceed limit", "extra", hexutil.Bytes(extra), "limit", params.GetMaximumExtraDataSize(isQuorum))
		extra = nil
	}
	return extra
}

// CreateConsensusEngine creates the required type of consensus engine instance for an Ethereum service
func CreateConsensusEngine(stack *node.Node, chainConfig *params.ChainConfig, config *Config, notify []string, noverify bool, db ethdb.Database) consensus.Engine {
	// If proof-of-authority is requested, set it up
	if chainConfig.Clique != nil {
		chainConfig.Clique.AllowedFutureBlockTime = config.Miner.AllowedFutureBlockTime //Quorum
		return clique.New(chainConfig.Clique, db)
	}
	// If Istanbul is requested, set it up
	if chainConfig.Istanbul != nil {
		if chainConfig.Istanbul.Epoch != 0 {
			config.Istanbul.Epoch = chainConfig.Istanbul.Epoch
		}
		config.Istanbul.ProposerPolicy = istanbul.ProposerPolicy(chainConfig.Istanbul.ProposerPolicy)
		config.Istanbul.Ceil2Nby3Block = chainConfig.Istanbul.Ceil2Nby3Block
		config.Istanbul.AllowedFutureBlockTime = config.Miner.AllowedFutureBlockTime //Quorum

		return istanbulBackend.New(&config.Istanbul, stack.GetNodeKey(), db)
	}

	// Otherwise assume proof-of-work
	switch config.Ethash.PowMode {
	case ethash.ModeFake:
		log.Warn("Ethash used in fake mode")
		return ethash.NewFaker()
	case ethash.ModeTest:
		log.Warn("Ethash used in test mode")
		return ethash.NewTester(nil, noverify)
	case ethash.ModeShared:
		log.Warn("Ethash used in shared mode")
		return ethash.NewShared()
	default:
		// For Quorum, Raft run as a separate service, so
		// the Ethereum service still needs a consensus engine,
		// use the consensus with the lightest overhead
		log.Warn("Ethash used in full fake mode")
		return ethash.NewFullFaker()
	}
}

// APIs return the collection of RPC services the ethereum package offers.
// NOTE, some of these services probably need to be moved to somewhere else.
func (s *Ethereum) APIs() []rpc.API {
	apis := ethapi.GetAPIs(s.APIBackend)

	// Append any APIs exposed explicitly by the consensus engine
	apis = append(apis, s.engine.APIs(s.BlockChain())...)

	// Append all the local APIs and return
	apis = append(apis, []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicEthereumAPI(s),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   NewPublicMinerAPI(s),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   downloader.NewPublicDownloaderAPI(s.protocolManager.downloader, s.eventMux),
			Public:    true,
		}, {
			Namespace: "miner",
			Version:   "1.0",
			Service:   NewPrivateMinerAPI(s),
			Public:    false,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   filters.NewPublicFilterAPI(s.APIBackend, false),
			Public:    true,
		}, {
			Namespace: "admin",
			Version:   "1.0",
			Service:   NewPrivateAdminAPI(s),
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPublicDebugAPI(s),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(s),
		}, {
			Namespace: "net",
			Version:   "1.0",
			Service:   s.netRPCService,
			Public:    true,
		},
	}...)
	return apis
}

func (s *Ethereum) ResetWithGenesisBlock(gb *types.Block) {
	s.blockchain.ResetWithGenesisBlock(gb)
}

func (s *Ethereum) Etherbase() (eb common.Address, err error) {
	s.lock.RLock()
	etherbase := s.etherbase
	s.lock.RUnlock()

	if etherbase != (common.Address{}) {
		return etherbase, nil
	}
	if wallets := s.AccountManager().Wallets(); len(wallets) > 0 {
		if accounts := wallets[0].Accounts(); len(accounts) > 0 {
			etherbase := accounts[0].Address

			s.lock.Lock()
			s.etherbase = etherbase
			s.lock.Unlock()

			log.Info("Etherbase automatically configured", "address", etherbase)
			return etherbase, nil
		}
	}
	return common.Address{}, fmt.Errorf("etherbase must be explicitly specified")
}

// isLocalBlock checks whether the specified block is mined
// by local miner accounts.
//
// We regard two types of accounts as local miner account: etherbase
// and accounts specified via `txpool.locals` flag.
func (s *Ethereum) isLocalBlock(block *types.Block) bool {
	author, err := s.engine.Author(block.Header())
	if err != nil {
		log.Warn("Failed to retrieve block author", "number", block.NumberU64(), "hash", block.Hash(), "err", err)
		return false
	}
	// Check whether the given address is etherbase.
	s.lock.RLock()
	etherbase := s.etherbase
	s.lock.RUnlock()
	if author == etherbase {
		return true
	}
	// Check whether the given address is specified by `txpool.local`
	// CLI flag.
	for _, account := range s.config.TxPool.Locals {
		if account == author {
			return true
		}
	}
	return false
}

// shouldPreserve checks whether we should preserve the given block
// during the chain reorg depending on whether the author of block
// is a local account.
func (s *Ethereum) shouldPreserve(block *types.Block) bool {
	// The reason we need to disable the self-reorg preserving for clique
	// is it can be probable to introduce a deadlock.
	//
	// e.g. If there are 7 available signers
	//
	// r1   A
	// r2     B
	// r3       C
	// r4         D
	// r5   A      [X] F G
	// r6    [X]
	//
	// In the round5, the inturn signer E is offline, so the worst case
	// is A, F and G sign the block of round5 and reject the block of opponents
	// and in the round6, the last available signer B is offline, the whole
	// network is stuck.
	if _, ok := s.engine.(*clique.Clique); ok {
		return false
	}
	return s.isLocalBlock(block)
}

// SetEtherbase sets the mining reward address.
func (s *Ethereum) SetEtherbase(etherbase common.Address) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.engine.(consensus.Istanbul); ok {
		log.Error("Cannot set etherbase in Istanbul consensus")
		return
	}
	s.etherbase = etherbase

	s.miner.SetEtherbase(etherbase)
}

// StartMining starts the miner with the given number of CPU threads. If mining
// is already running, this method adjust the number of threads allowed to use
// and updates the minimum price required by the transaction pool.
func (s *Ethereum) StartMining(threads int) error {
	// Update the thread count within the consensus engine
	type threaded interface {
		SetThreads(threads int)
	}
	if th, ok := s.engine.(threaded); ok {
		log.Info("Updated mining threads", "threads", threads)
		if threads == 0 {
			threads = -1 // Disable the miner from within
		}
		th.SetThreads(threads)
	}
	// If the miner was not running, initialize it
	if !s.IsMining() {
		// Propagate the initial price point to the transaction pool
		s.lock.RLock()
		price := s.gasPrice
		s.lock.RUnlock()
		s.txPool.SetGasPrice(price)

		// Configure the local mining address
		eb, err := s.Etherbase()
		if err != nil {
			log.Error("Cannot start mining without etherbase", "err", err)
			return fmt.Errorf("etherbase missing: %v", err)
		}
		if clique, ok := s.engine.(*clique.Clique); ok {
			wallet, err := s.accountManager.Find(accounts.Account{Address: eb})
			if wallet == nil || err != nil {
				log.Error("Etherbase account unavailable locally", "err", err)
				return fmt.Errorf("signer missing: %v", err)
			}
			clique.Authorize(eb, wallet.SignData)
		}
		// If mining is started, we can disable the transaction rejection mechanism
		// introduced to speed sync times.
		atomic.StoreUint32(&s.protocolManager.acceptTxs, 1)

		go s.miner.Start(eb)
	}
	return nil
}

// StopMining terminates the miner, both at the consensus engine level as well as
// at the block creation level.
func (s *Ethereum) StopMining() {
	// Update the thread count within the consensus engine
	type threaded interface {
		SetThreads(threads int)
	}
	if th, ok := s.engine.(threaded); ok {
		th.SetThreads(-1)
	}
	// Stop the block creating itself
	s.miner.Stop()
}

func (s *Ethereum) IsMining() bool      { return s.miner.Mining() }
func (s *Ethereum) Miner() *miner.Miner { return s.miner }

func (s *Ethereum) AccountManager() *accounts.Manager  { return s.accountManager }
func (s *Ethereum) BlockChain() *core.BlockChain       { return s.blockchain }
func (s *Ethereum) TxPool() *core.TxPool               { return s.txPool }
func (s *Ethereum) EventMux() *event.TypeMux           { return s.eventMux }
func (s *Ethereum) Engine() consensus.Engine           { return s.engine }
func (s *Ethereum) ChainDb() ethdb.Database            { return s.chainDb }
func (s *Ethereum) IsListening() bool                  { return true } // Always listening
func (s *Ethereum) EthVersion() int                    { return int(ProtocolVersions[0]) }
func (s *Ethereum) NetVersion() uint64                 { return s.networkID }
func (s *Ethereum) Downloader() *downloader.Downloader { return s.protocolManager.downloader }
func (s *Ethereum) Synced() bool                       { return atomic.LoadUint32(&s.protocolManager.acceptTxs) == 1 }
func (s *Ethereum) ArchiveMode() bool                  { return s.config.NoPruning }
func (s *Ethereum) BloomIndexer() *core.ChainIndexer   { return s.bloomIndexer }

// Quorum
// adds quorum specific protocols to the Protocols() function which in the associated upstream geth version returns
// only one subprotocol, "eth", and the supported versions of the "eth" protocol.
// Quorum uses the eth service to run configurable consensus protocols, e.g. istanbul. Thru release v20.10.0
// the "eth" subprotocol would be replaced with a modified subprotocol, e.g. "istanbul/99" which would contain all the "eth"
// messages + the istanbul message and be communicated over the consensus specific subprotocol ("istanbul"), and
// not over "eth".
// Now the eth service supports multiple protocols, e.g. "eth" and an optional consensus
// protocol, e.g. "istanbul/100".
// /Quorum

// Protocols returns all the currently configured
// network protocols to start.
func (s *Ethereum) Protocols() []p2p.Protocol {
	protos := make([]p2p.Protocol, len(ProtocolVersions))
	for i, vsn := range ProtocolVersions {
		protos[i] = s.protocolManager.makeProtocol(vsn)
		protos[i].Attributes = []enr.Entry{s.currentEthEntry()}
		protos[i].DialCandidates = s.dialCandidates
	}

	// /Quorum
	// add additional quorum consensus protocol if set and if not set to "eth", e.g. istanbul
	if quorumConsensusProtocolName != "" && quorumConsensusProtocolName != protocolName {
		quorumProtos := s.quorumConsensusProtocols()
		protos = append(protos, quorumProtos...)
	}
	// /end Quorum

	return protos
}

// Start implements node.Lifecycle, starting all internal goroutines needed by the
// Ethereum protocol implementation.
func (s *Ethereum) Start() error {
	s.startEthEntryUpdate(s.p2pServer.LocalNode())

	// Start the bloom bits servicing goroutines
	s.startBloomHandlers(params.BloomBitsBlocks)

	// Figure out a max peers count based on the server limits
	maxPeers := s.p2pServer.MaxPeers
	if s.config.LightServ > 0 {
		if s.config.LightPeers >= s.p2pServer.MaxPeers {
			return fmt.Errorf("invalid peer config: light peer count (%d) >= total peer count (%d)", s.config.LightPeers, s.p2pServer.MaxPeers)
		}
		maxPeers -= s.config.LightPeers
	}
	// Start the networking layer and the light server if requested
	s.protocolManager.Start(maxPeers)
	return nil
}

// Stop implements node.Lifecycle, terminating all internal goroutines used by the
// Ethereum protocol.
func (s *Ethereum) Stop() error {
	// Stop all the peer-related stuff first.
	s.protocolManager.Stop()

	// Then stop everything else.
	s.bloomIndexer.Close()
	close(s.closeBloomHandler)
	s.txPool.Stop()
	s.miner.Stop()
	s.blockchain.Stop()
	s.engine.Close()
	s.chainDb.Close()
	s.eventMux.Stop()
	return nil
}

func (s *Ethereum) CalcGasLimit(block *types.Block) uint64 {
	return core.CalcGasLimit(block, s.config.Miner.GasFloor, s.config.Miner.GasCeil)
}

// (Quorum)
// ConsensusServicePendingLogsFeed returns an event.Feed.  When the consensus protocol does not use eth.worker (e.g. raft), the event.Feed should be used to send logs from transactions included in the pending block
func (s *Ethereum) ConsensusServicePendingLogsFeed() *event.Feed {
	return s.consensusServicePendingLogsFeed
}

// (Quorum)
// SubscribePendingLogs starts delivering logs from transactions included in the consensus engine's pending block to the given channel.
func (s *Ethereum) SubscribePendingLogs(ch chan<- []*types.Log) event.Subscription {
	if s.config.RaftMode {
		return s.consensusServicePendingLogsFeed.Subscribe(ch)
	}
	return s.miner.SubscribePendingLogs(ch)
}
