package config

import (
	"os"
	"path"
)

func InitDefaultCfg() KernelConf {
	return InitDefaultCfgWithDir("")
}

func InitDefaultCfgWithDir(dir string) KernelConf {
	if dir != "" {
		err := os.MkdirAll(dir, 0700)
		if err != nil {
			panic(err)
		}
	}

	cfg := KernelConf{
		RunMode:   0,
		HttpPort:  "7999",
		WsPort:    "8999",
		LogLevel:  "info",
		LogOutput: "yu.log",
		LeiLimit:  50000,
		Timeout:   60,
	}
	cfg.P2P = P2pConf{
		P2pListenAddrs:  []string{"/ip4/127.0.0.1/tcp/8887"},
		Bootnodes:       nil,
		ProtocolID:      "yu",
		NodeKeyType:     1,
		NodeKeyRandSeed: 1,
		NodeKey:         "",
		NodeKeyBits:     0,
		NodeKeyFile:     "",
	}
	cfg.BlockChain = BlockchainConf{
		ChainDB: SqlDbConf{
			SqlDbType: "sqlite",
			Dsn:       path.Join(dir, "chain.db"),
		},
	}
	cfg.YuDB = YuDBConf{
		BaseDB: SqlDbConf{
			SqlDbType: "sqlite",
			Dsn:       path.Join(dir, "blockbase.db"),
		}}
	cfg.Txpool = TxpoolConf{
		PoolSize:   2048,
		TxnMaxSize: 1024000,
	}
	cfg.State = StateConf{KV: StateKvConf{
		IndexDB: KVconf{
			KvType: "bolt",
			Path:   path.Join(dir, "state_index.db"),
			Hosts:  nil,
		},
		NodeBase: KVconf{
			KvType: "bolt",
			Path:   path.Join(dir, "state_base.db"),
			Hosts:  nil,
		},
	}}
	return cfg
}
