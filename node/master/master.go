package master

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/libp2p/go-libp2p-core/host"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/sirupsen/logrus"
	"math/rand"
	"net/http"
	"sync"
	"time"
	. "yu/blockchain"
	. "yu/common"
	. "yu/config"
	. "yu/node"
	"yu/storage/kv"
	. "yu/tripod"
	. "yu/txn"
	. "yu/txpool"
	. "yu/utils/ip"
	. "yu/yerror"
)

type Master struct {
	sync.Mutex

	host host.Host
	ps   *pubsub.PubSub

	RunMode RunMode
	// Key: NodeKeeper IP, Value: NodeKeeperInfo
	nkDB     kv.KV
	httpPort string
	wsPort   string
	timeout  time.Duration

	chain  IBlockChain
	base   IBlockBase
	txPool ItxPool
	land   *Land

	// blocks to broadcast into P2P network
	// blockBcChan chan *TransferBody

	// ready to package a batch of txns to broadcast
	// readyBcTxnsChan -> txnsBcChan -> P2P network
	// readyBcTxnsChan chan *SignedTxn
	// number of broadcast txns every time
	// NumOfBcTxns int

	// txns to broadcast into P2P network
	// txnsBcChan chan *TransferBody

	// event subscription
	sub *Subscription

	// p2p topics
	blockTopic        *pubsub.Topic
	packedTxnsTopic   *pubsub.Topic
	unpackedTxnsTopic *pubsub.Topic
}

func NewMaster(
	cfg *MasterConf,
	chain IBlockChain,
	base IBlockBase,
	txPool ItxPool,
	land *Land,
) (*Master, error) {
	nkDB, err := kv.NewKV(&cfg.NkDB)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	p2pHost, err := makeP2pHost(ctx, cfg)
	if err != nil {
		return nil, err
	}

	ps, err := pubsub.NewGossipSub(ctx, p2pHost)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(cfg.Timeout) * time.Second

	m := &Master{
		host:     p2pHost,
		ps:       ps,
		RunMode:  cfg.RunMode,
		nkDB:     nkDB,
		timeout:  timeout,
		httpPort: MakePort(cfg.HttpPort),
		wsPort:   MakePort(cfg.WsPort),
		chain:    chain,
		base:     base,
		txPool:   txPool,
		land:     land,
		sub:      NewSubscription(),
	}
	err = m.ConnectP2PNetwork(cfg)
	if err != nil {
		return nil, err
	}
	err = m.initTopics()
	return m, err
}

func (m *Master) P2pID() string {
	return m.host.ID().String()
}

func (m *Master) Startup() {

	switch m.RunMode {
	case LocalNode:
		err := m.land.RangeList(func(tri Tripod) error {
			return tri.InitChain(m.chain, m.base)
		})
		if err != nil {
			logrus.Panicf("init chain error: %s", err.Error())
		}
	case MasterWorker:
		// todo: init chain

		go m.CheckHealth()
	}

	go m.HandleHttp()
	go m.HandleWS()

	go func() {
		for {
			err := m.AcceptBlocksFromP2P()
			if err != nil {
				logrus.Errorf("accept blocks error: %s", err.Error())
			}
		}

	}()

	go func() {
		for {
			err := m.AcceptUnpkgTxns()
			if err != nil {
				logrus.Errorf("accept unpacked txns error: %s", err.Error())
			}
		}

	}()

	m.Run()
}

// Check the health of NodeKeepers by SendHeartbeat to them.
func (m *Master) CheckHealth() {
	for {
		nkAddrs, err := m.allNodeKeepersIp()
		if err != nil {
			logrus.Errorf("get all NodeKeepers error: %s", err.Error())
		}
		SendHeartbeats(
			nkAddrs,
			func(ip string) error {
				return m.setNkIfOnline(ip, true)
			},
			func(ip string) error {
				return m.setNkIfOnline(ip, false)
			})
		time.Sleep(m.timeout)
	}
}

// FIXME: when number of txns is just less than NumOfBcTxns
//func (m *Master) BroadcastTxns() {
//	var txns SignedTxns
//	for {
//		select {
//		case txn := <-m.readyBcTxnsChan:
//			txns = append(txns, txn)
//			if len(txns) == m.NumOfBcTxns {
//				body, err := NewTxnsTransferBody(txns)
//				if err != nil {
//					logrus.Errorf("new TxnTransferBody error: %s", err.Error())
//					continue
//				}
//				m.txnsBcChan <- body
//				txns = nil
//			}
//		}
//	}
//}

// sync P2P-network's txns
func (m *Master) SyncTxns(block IBlock) error {
	txnsHashes := block.GetTxnsHashes()
	blockHash := block.GetHeader().GetHash()
	txns, err := m.base.GetTxns(blockHash)
	if err != nil {
		return err
	}
	needFetch := make([]Hash, 0)
	for _, txnHash := range txnsHashes {
		_, exist := existTxnHash(txnHash, txns)
		if !exist {
			needFetch = append(needFetch, txnHash)
		}
	}

	if len(needFetch) > 0 {
		blockHash, allTxns, err := m.subPackedTxns()
		if err != nil {
			return err
		}
		fetchedTxns := make([]*SignedTxn, 0)
		for _, txnHash := range needFetch {
			stxn, exist := existTxnHash(txnHash, allTxns)
			if !exist {
				return NoTxnInP2P(txnHash)
			}
			fetchedTxns = append(fetchedTxns, stxn)
		}

		for _, fetchedTxn := range fetchedTxns {
			err = m.txPool.NecessaryCheck(fetchedTxn)
			if err != nil {
				return err
			}
		}

		return m.base.SetTxns(blockHash, fetchedTxns)
	}

	return nil
}

func (m *Master) SyncHistoryBlocks(blocks []IBlock) error {
	switch m.RunMode {
	case LocalNode:
		for _, block := range blocks {
			err := m.land.RangeList(func(tri Tripod) error {
				if tri.ValidateBlock(m.chain, block) {
					return m.chain.AppendBlock(block)
				}
				return BlockIllegal(block.GetHeader().GetHash())
			})
			if err != nil {
				return err
			}
		}

		return m.executeChainTxns()

	case MasterWorker:
		// todo
		return nil
	default:
		return NoRunMode
	}
}

func (m *Master) executeChainTxns() error {
	chain, err := m.chain.Chain()
	if err != nil {
		return err
	}
	return chain.Range(func(block IBlock) error {
		return ExecuteTxns(block, m.base, m.land, nil)
	})
}

func (m *Master) registerNodeKeepers(c *gin.Context) {
	m.Lock()
	defer m.Unlock()
	var nkInfo NodeKeeperInfo
	err := c.ShouldBindJSON(&nkInfo)
	if err != nil {
		c.String(
			http.StatusBadRequest,
			fmt.Sprintf("NodeKeeperInfo decode failed: %s", err.Error()),
		)
		return
	}
	nkIP := MakeIp(c.ClientIP(), nkInfo.ServesPort)

	err = m.SetNodeKeeper(nkIP, nkInfo)
	if err != nil {
		c.String(
			http.StatusInternalServerError,
			fmt.Sprintf("store NodeKeeper(%s) error: %s", nkIP, err.Error()),
		)
		return
	}

	c.String(http.StatusOK, "")
	logrus.Infof("NodeKeeper(%s) register succeed!", nkIP)
}

func (m *Master) SetNodeKeeper(ip string, info NodeKeeperInfo) error {
	infoByt, err := info.EncodeNodeKeeperInfo()
	if err != nil {
		return err
	}
	return m.nkDB.Set([]byte(ip), infoByt)
}

func (m *Master) allNodeKeepersIp() ([]string, error) {
	nkIPs := make([]string, 0)
	err := m.allNodeKeepers(func(ip string, _ *NodeKeeperInfo) error {
		nkIPs = append(nkIPs, ip)
		return nil
	})
	return nkIPs, err
}

func (m *Master) WorkersCount() (int, error) {
	count := 0
	err := m.allNodeKeepers(func(_ string, info *NodeKeeperInfo) error {
		count += len(info.WorkersInfo)
		return nil
	})
	return count, err
}

func (m *Master) randomGetWorkerIP() (string, error) {
	ips, err := m.allWorkersIP()
	if err != nil {
		return "", err
	}
	randIdx := rand.Intn(len(ips))
	return ips[randIdx], nil
}

func (m *Master) allWorkersIP() ([]string, error) {
	var workersIP []string
	err := m.allNodeKeepers(func(_ string, info *NodeKeeperInfo) error {
		for ip, _ := range info.WorkersInfo {
			workersIP = append(workersIP, ip)
		}
		return nil
	})
	return workersIP, err
}

// find workerIP by Execution/Query name
func (m *Master) findWorkerIP(tripodName, callName string, callType CallType) (wip string, err error) {
	wip, _, err = m.findWorker(tripodName, callName, callType)
	return
}

func (m *Master) findWorkerIpAndName(tripodName, callName string, callType CallType) (wip, name string, err error) {
	var info *WorkerInfo
	wip, info, err = m.findWorker(tripodName, callName, callType)
	if err != nil {
		return
	}
	name = info.Name
	return
}

func (m *Master) findWorker(tripodName, callName string, callType CallType) (wip string, wInfo *WorkerInfo, err error) {
	err = m.allNodeKeepers(func(nkIP string, info *NodeKeeperInfo) error {
		if !info.Online {
			return NodeKeeperDead(nkIP)
		}
		for workerIp, workerInfo := range info.WorkersInfo {
			if !workerInfo.Online {
				return WorkerDead(workerInfo.Name)
			}
			triInfo, ok := workerInfo.TripodsInfo[tripodName]
			if !ok {
				return TripodNotFound(tripodName)
			}
			var callArr []string
			switch callType {
			case ExecCall:
				callArr = triInfo.ExecNames
			case QryCall:
				callArr = triInfo.QueryNames
			}
			for _, call := range callArr {
				if call == callName {
					wip = workerIp
					wInfo = &workerInfo
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		return
	}
	if wip == "" || wInfo == nil {
		switch callType {
		case ExecCall:
			err = ExecNotFound(callName)
		case QryCall:
			err = QryNotFound(callName)
		}
	}
	return
}

func (m *Master) allNodeKeepers(fn func(nkIP string, info *NodeKeeperInfo) error) error {
	iter, err := m.nkDB.Iter(nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.Valid() {
		ipByt, infoByt, err := iter.Entry()
		if err != nil {
			return err
		}
		ip := string(ipByt)
		info, err := DecodeNodeKeeperInfo(infoByt)
		if err != nil {
			return err
		}
		err = fn(ip, info)
		if err != nil {
			return err
		}
		err = iter.Next()
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Master) setNkIfOnline(ip string, isOnline bool) error {
	tx, err := m.nkDB.NewKvTxn()
	if err != nil {
		return err
	}
	info, err := getNkWithTx(tx, ip)
	if err != nil {
		return err
	}
	if info.Online != isOnline {
		info.Online = isOnline
		err = setNkWithTx(tx, ip, info)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func getNkWithTx(txn kv.KvTxn, ip string) (*NodeKeeperInfo, error) {
	infoByt, err := txn.Get([]byte(ip))
	if err != nil {
		return nil, err
	}
	return DecodeNodeKeeperInfo(infoByt)
}

func setNkWithTx(txn kv.KvTxn, ip string, info *NodeKeeperInfo) error {
	infoByt, err := info.EncodeNodeKeeperInfo()
	if err != nil {
		return err
	}
	return txn.Set([]byte(ip), infoByt)
}

func existTxnHash(txnHash Hash, txns []*SignedTxn) (*SignedTxn, bool) {
	for _, stxn := range txns {
		if stxn.GetTxnHash() == txnHash {
			return stxn, true
		}
	}
	return nil, false
}
