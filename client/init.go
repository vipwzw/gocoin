package main

import (
	"os"
	"fmt"
	"time"
	"encoding/hex"
	"github.com/vipwzw/gocoin/btc"
	"github.com/vipwzw/gocoin/others/blockdb"
	"github.com/vipwzw/gocoin/client/common"
	"github.com/vipwzw/gocoin/client/wallet"
	"github.com/vipwzw/gocoin/client/network"
	"github.com/vipwzw/gocoin/client/usif/textui"
	"github.com/vipwzw/gocoin/others/utils"
)


func host_init() {
	var e error
	BtcRootDir := utils.BitcoinHome()

	if common.CFG.Datadir == "" {
		common.GocoinHomeDir = BtcRootDir+"gocoin"+string(os.PathSeparator)
	} else {
		common.GocoinHomeDir = common.CFG.Datadir+string(os.PathSeparator)
	}

	common.Testnet = common.CFG.Testnet // So chaging this value would will only affect the behaviour after restart
	if common.CFG.Testnet { // testnet3
		common.GenesisBlock = btc.NewUint256FromString("000000000933ea01ad0ee984209779baaec3ced90fa3f408719526f8d77f4943")
		common.Magic = [4]byte{0x0B,0x11,0x09,0x07}
		common.GocoinHomeDir += "tstnet"+string(os.PathSeparator)
		BtcRootDir += "testnet3"+string(os.PathSeparator)
		network.AlertPubKey, _ = hex.DecodeString("04302390343f91cc401d56d68b123028bf52e5fca1939df127f63c6467cdf9c8e2c14b61104cf817d0b780da337893ecc4aaff1309e536162dabbdb45200ca2b0a")
		common.MaxPeersNeeded = 100
	} else {
		common.GenesisBlock = btc.NewUint256FromString("00000b75d032c3049bd2626a7fddefbd3ad31ff5a8e272655b4b2219177a34c2")
		common.Magic = [4]byte{0xF9,0xBE,0xB4,0xD9}
		common.GocoinHomeDir += "btcnet"+string(os.PathSeparator)
		network.AlertPubKey, _ = hex.DecodeString("04fc9702847840aaf195de8442ebecedf5b095cdbb9bc716bda9110971b28a49e0ead8564ff0db22209e0374782c093bb899692d524e9d6a6956e7c5ecbcd68284")
		common.MaxPeersNeeded = 1000
	}

	// Lock the folder
	os.MkdirAll(common.GocoinHomeDir, 0770)
	os.MkdirAll(common.GocoinHomeDir+"wallet", 0770)
	utils.LockDatabaseDir(common.GocoinHomeDir)

	fi, e := os.Stat(common.GocoinHomeDir+"blockchain.dat")
	if e!=nil {
		os.RemoveAll(common.GocoinHomeDir)
		fmt.Println("You seem to be running Gocoin for the fist time on this PC")
		fi, e = os.Stat(BtcRootDir+"blocks/blk00000.dat")
		if e==nil && fi.Size()>1024*1024 {
			fmt.Println("There is a database from Satoshi client on your disk...")
			if textui.AskYesNo("Do you want to import this database into Gocoin?") {
				import_blockchain(BtcRootDir+"blocks")
			}
		}
	}

	fmt.Println("Opening blockchain... (Ctrl-C to interrupt)")

	__exit := make(chan bool)
	__done := make(chan bool)
	go func() {
		for {
			select {
				case s := <-killchan:
					fmt.Println(s)
					btc.AbortNow = true
				case <-__exit:
					__done <- true
					return
			}
		}
	}()
	sta := time.Now().UnixNano()
	common.BlockChain = btc.NewChain(common.GocoinHomeDir, common.GenesisBlock, common.FLAG.Rescan)
	sto := time.Now().UnixNano()
	if btc.AbortNow {
		fmt.Printf("Blockchain opening aborted after %.3f seconds\n", float64(sto-sta)/1e9)
		common.BlockChain.Close()
		utils.UnlockDatabaseDir()
		os.Exit(1)
	}
	fmt.Printf("Blockchain open in %.3f seconds\n", float64(sto-sta)/1e9)
	common.BlockChain.Unspent.SetTxNotify(wallet.TxNotify)
	common.StartTime = time.Now()
	__exit <- true
	_ = <- __done
}


func stat(totnsec, pernsec int64, totbytes, perbytes uint64, height uint32) {
	totmbs := float64(totbytes) / (1024*1024)
	perkbs := float64(perbytes) / (1024)
	var x string
	if btc.EcdsaVerifyCnt > 0 {
		x = fmt.Sprintf("|  %d -> %d us/ecdsa", btc.EcdsaVerifyCnt, uint64(pernsec)/btc.EcdsaVerifyCnt/1e3)
		btc.EcdsaVerifyCnt = 0
	}
	fmt.Printf("%.1fMB of data processed. We are at height %d. Processing speed %.3fMB/sec, recent: %.1fKB/s %s\n",
		totmbs, height, totmbs/(float64(totnsec)/1e9), perkbs/(float64(pernsec)/1e9), x)
}


func import_blockchain(dir string) {
	trust := !textui.AskYesNo("Do you want to verify scripts while importing (will be slow)?")

	BlockDatabase := blockdb.NewBlockDB(dir, common.Magic)
	chain := btc.NewChain(common.GocoinHomeDir, common.GenesisBlock, false)

	var bl *btc.Block
	var er error
	var dat []byte
	var totbytes, perbytes uint64

	chain.DoNotSync = true

	fmt.Println("Be patient while importing Satoshi's database... ")
	start := time.Now().UnixNano()
	prv := start
	for {
		now := time.Now().UnixNano()
		if now-prv >= 10e9 {
			stat(now-start, now-prv, totbytes, perbytes, chain.BlockTreeEnd.Height)
			prv = now  // show progress each 10 seconds
			perbytes = 0
		}

		dat, er = BlockDatabase.FetchNextBlock()
		if dat==nil || er!=nil {
			println("END of DB file")
			break
		}

		bl, er = btc.NewBlock(dat[:])
		if er != nil {
			println("Block inconsistent:", er.Error())
			break
		}

		bl.Trusted = trust

		er, _, _ = chain.CheckBlock(bl)

		if er != nil {
			if er.Error()!="Genesis" {
				println("CheckBlock failed:", er.Error())
				//os.Exit(1) // Such a thing should not happen, so let's better abort here.
			}
			continue
		}

		er = chain.AcceptBlock(bl)
		if er != nil {
			println("AcceptBlock failed:", er.Error())
			//os.Exit(1) // Such a thing should not happen, so let's better abort here.
		}

		totbytes += uint64(len(bl.Raw))
		perbytes += uint64(len(bl.Raw))
	}

	stop := time.Now().UnixNano()
	stat(stop-start, stop-prv, totbytes, perbytes, chain.BlockTreeEnd.Height)

	fmt.Println("Satoshi's database import finished in", (stop-start)/1e9, "seconds")

	fmt.Println("Now saving the new database...")
	chain.Sync()
	chain.Save()
	chain.Close()
	fmt.Println("Database saved. No more imports should be needed.")
	fmt.Println("It is advised to close and restart the node now, to free some mem.")
}
