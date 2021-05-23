package pow

import (
	"github.com/sirupsen/logrus"
	"math/big"
	"time"
	. "yu/blockchain"
	. "yu/chain_env"
	. "yu/common"
	spow "yu/consensus/pow"
	"yu/node"
	. "yu/tripod"
	"yu/txn"
	ytime "yu/utils/time"
)

type Pow struct {
	meta       *TripodMeta
	target     *big.Int
	targetBits int64

	pkgTxnsLimit uint64
}

func NewPow(pkgTxnsLimit uint64) *Pow {
	meta := NewTripodMeta("pow")
	var targetBits int64 = 16
	target := big.NewInt(1)
	target.Lsh(target, uint(256-targetBits))
	return &Pow{
		meta:         meta,
		target:       target,
		targetBits:   targetBits,
		pkgTxnsLimit: pkgTxnsLimit,
	}
}

func newDefaultBlock() *Block {
	return &Block{
		Header: &Header{
			Timestamp: ytime.NowNanoTsU64(),
		},
	}
}

func (p *Pow) TripodMeta() *TripodMeta {
	return p.meta
}

func (*Pow) CheckTxn(*txn.SignedTxn) error {
	return nil
}

func (p *Pow) ValidateBlock(block IBlock, env *ChainEnv) bool {
	return spow.Validate(block, p.target, p.targetBits)
}

func (*Pow) InitChain(env *ChainEnv, _ *Land) error {
	chain := env.Chain
	gensisBlock := newDefaultBlock()
	gensisBlock.SetHeight(0)
	return chain.SetGenesis(gensisBlock)
}

func (p *Pow) StartBlock(env *ChainEnv, _ *Land) (block IBlock, needBroadcast bool, err error) {
	time.Sleep(2 * time.Second)

	block = newDefaultBlock()
	chain := env.Chain
	pool := env.Pool

	prevBlock, err := chain.GetEndBlock()
	if err != nil {
		return
	}

	prevHeight := prevBlock.GetHeader().GetHeight()
	prevHash := prevBlock.GetHeader().GetHash()

	logrus.Infof("prev-block hash is (%s), height is (%d)", prevHash.String(), prevHeight)

	height := prevHeight + 1

	pbMap, err := chain.TakeP2pBlocksBefore(height)
	if err != nil {
		logrus.Errorf("get p2p-blocks before error: %s", err.Error())
	}

	for _, pbs := range pbMap {
		for _, pb := range pbs {
			err = chain.AppendBlock(pb)
			if err != nil {
				return
			}
		}
	}

	pbsht, err := chain.TakeP2pBlocks(height)
	if err != nil {
		logrus.Errorf("get p2p-blocks error: %s", err.Error())
	}
	if len(pbsht) > 0 {
		block.CopyFrom(pbsht[0])
		logrus.Infof("USE P2P block(%s)", block.GetHeader().GetHash().String())
		return
	}

	needBroadcast = true

	block.SetPreHash(prevHash)
	block.SetHeight(height)

	txns, err := pool.Package("", p.pkgTxnsLimit)
	if err != nil {
		return
	}
	txnsHashes := make([]Hash, 0)
	for _, hash := range txnsHashes {
		txnsHashes = append(txnsHashes, hash)
	}
	block.SetTxnsHashes(txnsHashes)

	txnRoot, err := MakeTxnRoot(txns)
	if err != nil {
		return
	}
	block.SetStateRoot(txnRoot)

	nonce, hash, err := spow.Run(block, p.target, p.targetBits)
	if err != nil {
		return
	}
	block.(*Block).SetNonce(uint64(nonce))
	block.SetHash(hash)

	return
}

func (*Pow) EndBlock(block IBlock, env *ChainEnv, land *Land) error {
	chain := env.Chain
	pool := env.Pool

	err := node.ExecuteTxns(block, env, land)
	if err != nil {
		return err
	}

	err = chain.AppendBlock(block)
	if err != nil {
		return err
	}

	logrus.Infof("append block(%d)", block.GetHeader().GetHeight())

	return pool.Flush()
}

func (*Pow) FinalizeBlock(_ IBlock, _ *ChainEnv, _ *Land) error {
	return nil
}
