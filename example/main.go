package main

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/Ch-iron/go-evm/db/rawdb"
	"github.com/Ch-iron/go-evm/state"
	"github.com/Ch-iron/go-evm/state/snapshot"
	"github.com/Ch-iron/go-evm/state/tracing"
	"github.com/Ch-iron/go-evm/triedb"
	"github.com/Ch-iron/go-evm/triedb/hashdb"
	"github.com/Ch-iron/go-evm/types"
	"github.com/Ch-iron/go-evm/vm"
	"github.com/Ch-iron/go-evm/vm/runtime"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// In order to use the latest opcodes, blocks after the Ethereum Shanghai update need to be set up in the EVM.
const SHANGHAI_BLOCK_COUNT = 16830000

func loadBin(filename string) []byte {
	code, _ := os.ReadFile(filename)
	return hexutil.MustDecode("0x" + string(code))
}

func loadAbi(filename string) abi.ABI {
	abiFile, _ := os.Open(filename)
	defer abiFile.Close()
	abiObj, _ := abi.JSON(abiFile)
	return abiObj
}

func SetDefaults(blockheight int) *runtime.Config {
	cfg := new(runtime.Config)
	if cfg.Difficulty == nil {
		cfg.Difficulty = big.NewInt(0)
	}
	if cfg.GasLimit == 0 {
		cfg.GasLimit = math.MaxUint64
	}
	if cfg.GasPrice == nil {
		cfg.GasPrice = new(big.Int)
	}
	if cfg.Value == nil {
		cfg.Value = new(big.Int)
	}
	if cfg.GetHashFn == nil {
		cfg.GetHashFn = func(n uint64) common.Hash {
			return common.BytesToHash(crypto.Keccak256([]byte(new(big.Int).SetUint64(n).String())))
		}
	}
	if cfg.BaseFee == nil {
		cfg.BaseFee = big.NewInt(params.InitialBaseFee)
	}
	if cfg.BlobBaseFee == nil {
		cfg.BlobBaseFee = big.NewInt(params.BlobTxMinBlobGasprice)
	}

	cfg.ChainConfig = params.SepoliaChainConfig

	// Decide evm version
	// EVM version is must more than shanghai because recent solidity compile has PUSH0 opcode
	cfg.BlockNumber = big.NewInt(SHANGHAI_BLOCK_COUNT + int64(blockheight))
	cfg.Time = uint64(time.Now().Unix())
	random := common.BytesToHash([]byte("Ch-iron"))
	cfg.Random = &random

	return cfg
}

func main() {
	// Get test address
	addrs, err := os.ReadFile("./address.txt")
	if err != nil {
		panic(err)
	}

	var addrArray []common.Address

	addrArrayString := strings.Split(string(addrs), "\n")
	for i := 0; i < len(addrArrayString); i++ {
		stringtoaddress := common.HexToAddress(addrArrayString[i])
		addrArray = append(addrArray, stringtoaddress)
	}

	logFile, _ := os.Create("log.txt")
	syscall.Dup2(int(logFile.Fd()), 2)

	cfg := SetDefaults(0)

	// If you use snapshot, this config must be used
	snapconfig := snapshot.Config{
		CacheSize:  256,
		Recovery:   true,
		NoBuild:    false,
		AsyncBuild: false,
	}

	triedbConfig := &triedb.Config{
		Preimages: true,
		HashDB: &hashdb.Config{
			CleanCacheSize: 256 * 1024 * 1024,
		},
	}

	// You can select memorydb, leveldb, pebbledb
	// Leveldb and Pebbledb is permanent database on disk
	// If you use permanent database, you must set database path
	leveldb, _ := rawdb.NewLevelDBDatabase("../leveldb", 128, 1024, "", false)
	// memdb := rawdb.NewMemoryDatabase()
	// pebbledb, _ := rawdb.NewPebbleDBDatabase("../pebbledb", 128, 1024, "", false)

	// Create State
	tdb := triedb.NewDatabase(leveldb, triedbConfig)
	snaps, _ := snapshot.New(snapconfig, leveldb, tdb, types.EmptyRootHash)
	statedb := state.NewDatabase(tdb, snaps) // If you don't use snapshot, snaps is null
	globalstate, _ := state.New(types.EmptyRootHash, statedb)

	// Set address's balance
	for i := 0; i < len(addrArray); i++ {
		globalstate.CreateAccount(addrArray[i])
		globalstate.SetBalance(addrArray[i], uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
		globalstate.SetNonce(addrArray[i], 0)
	}

	// Get abi, bin
	abiObj := loadAbi("./Deposit.abi")
	data := loadBin("./Deposit.bin")

	evm := runtime.NewEnv(cfg, globalstate)
	testAddress := common.BytesToAddress([]byte("Ch-iron"))
	testAddress2 := common.BytesToAddress([]byte("Chiron"))
	globalstate.SetBalance(testAddress, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
	globalstate.SetBalance(testAddress2, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)

	// Transfer Example
	evm.Context.Transfer(globalstate, testAddress, testAddress2, uint256.NewInt(100000))
	fmt.Printf("testAddress Balance: %v, testAddress2 Balance: %v\n", globalstate.GetBalance(testAddress), globalstate.GetBalance(testAddress2))
	time.Sleep(1 * time.Second)

	// Deposit Contract Deploy
	sender := vm.AccountRef(testAddress)

	_, contractAddress, gasleftover, err := evm.Create(
		sender,
		data,
		globalstate.GetBalance(testAddress).Uint64(),
		uint256.NewInt(0),
	)
	if err != nil {
		panic(err)
	}
	globalstate.SetBalance(testAddress, uint256.NewInt(gasleftover), tracing.BalanceChangeUnspecified)

	// Modified State commit into disk
	root, _ := globalstate.Commit(0, true, 128)
	if err := tdb.Commit(root, false); err != nil {
		panic(err)
	}
	leveldb.Close()

	// The reason for closing and opening leveldb is to make sure that the commit was successful and saved to disk.

	// Deposit balance
	leveldb, _ = rawdb.NewLevelDBDatabase("../leveldb", 128, 1024, "", false)
	tdb = triedb.NewDatabase(leveldb, triedbConfig)
	snaps, _ = snapshot.New(snapconfig, leveldb, tdb, root)
	statedb = state.NewDatabase(tdb, snaps)
	globalstate, _ = state.New(root, statedb)

	input, err := abiObj.Pack("deposit")

	// Deposit balance EVM sequential execute version
	evm = runtime.NewEnv(cfg, globalstate)
	if err != nil {
		panic(err)
	}

	for i := 0; i < len(addrArray); i++ {
		account := addrArray[i]
		sender := vm.AccountRef(account)

		_, gasleftover, err = evm.Call(
			sender,
			contractAddress,
			input,
			globalstate.GetBalance(account).Uint64(),
			uint256.NewInt(1e18-100000),
		)
		if err != nil {
			panic(err)
		}
		globalstate.SetBalance(addrArray[i], uint256.NewInt(gasleftover-uint64(1e18-100000)), tracing.BalanceChangeUnspecified)
	}

	// Modified State commit into disk
	root, _ = globalstate.Commit(0, true, 128)
	if err := tdb.Commit(root, false); err != nil {
		panic(err)
	}
	leveldb.Close()

	// Verify Balance
	leveldb, _ = rawdb.NewLevelDBDatabase("../leveldb", 128, 1024, "", false)
	tdb = triedb.NewDatabase(leveldb, triedbConfig)
	snaps, _ = snapshot.New(snapconfig, leveldb, tdb, root)
	statedb = state.NewDatabase(tdb, snaps)
	globalstate, _ = state.New(root, statedb)

	evm = runtime.NewEnv(cfg, globalstate)

	for i := 0; i < len(addrArray); i++ {
		account := addrArray[i]
		sender := vm.AccountRef(account)
		input, err := abiObj.Pack("verifyBalance", account)
		if err != nil {
			panic(err)
		}

		returnBalance, _, err := evm.Call(
			sender,
			contractAddress,
			input,
			globalstate.GetBalance(account).Uint64(),
			uint256.NewInt(0),
		)
		if err != nil {
			fmt.Printf("%v\n", err)
			panic(err)
		}
		encodedString := hex.EncodeToString(returnBalance)
		n := new(big.Int)
		n.SetString(encodedString, 16)
		fmt.Printf("%vth Account: %v, Deposited Balance: %v, Remained Balance: %v\n", i+1, account, n, globalstate.GetBalance(account))
	}

	leveldb.Close()
}
