package main

import (
	"os"
	"fmt"
	"bytes"
	"bufio"
	"strconv"
	"strings"
	"encoding/hex"
	"github.com/piotrnar/gocoin/btc"
)

// load the content of the "balance/" folder
func load_balance(showbalance bool) {
	var unknownInputs, multisigInputs int
	f, e := os.Open("balance/unspent.txt")
	if e != nil {
		println(e.Error())
		return
	}
	rd := bufio.NewReader(f)
	for {
		l, _, e := rd.ReadLine()
		if len(l)==0 && e!=nil {
			break
		}
		if l[64]=='-' {
			txid := btc.NewUint256FromString(string(l[:64]))
			rst := strings.SplitN(string(l[65:]), " ", 2)
			vout, _ := strconv.ParseUint(rst[0], 10, 32)
			uns := new(btc.TxPrevOut)
			copy(uns.Hash[:], txid.Hash[:])
			uns.Vout = uint32(vout)
			lab := ""
			if len(rst)>1 {
				lab = rst[1]
			}

			str := string(l)
			if sti:=strings.Index(str, "_StealthC:"); sti!=-1 {
				c, e := hex.DecodeString(str[sti+10:sti+10+64])
				if e != nil {
					fmt.Println("ERROR at stealth", txid.String(), vout, e.Error())
				} else {
					// add a new key to the wallet
					sec := btc.DeriveNextPrivate(first_seed[:], c)
					is_stealth[len(priv_keys)] = true
					priv_keys = append(priv_keys, sec)
					labels = append(labels, lab)
					pub_key := btc.PublicFromPrivate(sec, true)
					publ_addrs = append(publ_addrs, btc.NewAddrFromPubkey(pub_key, btc.AddrVerPubkey(*testnet)))
					compressed_key = append(compressed_key, true) // stealth keys are always compressed
				}
			}

			if _, ok := loadedTxs[txid.Hash]; !ok {
				tf, _ := os.Open("balance/"+txid.String()+".tx")
				if tf != nil {
					siz, _ := tf.Seek(0, os.SEEK_END)
					tf.Seek(0, os.SEEK_SET)
					buf := make([]byte, siz)
					tf.Read(buf)
					tf.Close()
					th := btc.Sha2Sum(buf)
					if bytes.Equal(th[:], txid.Hash[:]) {
						tx, _ := btc.NewTx(buf)
						if tx != nil {
							loadedTxs[txid.Hash] = tx
						} else {
							println("transaction is corrupt:", txid.String())
						}
					} else {
						println("transaction file is corrupt:", txid.String())
						os.Exit(1)
					}
				} else {
					println("transaction file not found:", txid.String())
					os.Exit(1)
				}
			}

			// Sum up all the balance and check if we have private key for this input
			uo := UO(uns)

			add_it := true

			if !btc.IsP2SH(uo.Pk_script) {
				fnd := false
				for j := range publ_addrs {
					if publ_addrs[j].Owns(uo.Pk_script) {
						fnd = true
						break
					}
				}

				if !fnd {
					if *onlvalid {
						add_it = false
					}
					if showbalance {
						unknownInputs++
						if *verbose {
							ss := uns.String()
							ss = ss[:8]+"..."+ss[len(ss)-12:]
							fmt.Println(ss, "does not belong to your wallet (cannot sign it)")
						}
					}
				}
			} else {
				if *onlvalid {
					add_it = false
				}
				if *verbose {
					ss := uns.String()
					ss = ss[:8]+"..."+ss[len(ss)-12:]
					fmt.Println(ss, "belongs to a multisig address")
				}
				multisigInputs++
			}

			if add_it {
				unspentOuts = append(unspentOuts, uns)
				unspentOutsLabel = append(unspentOutsLabel, lab)
				totBtc += UO(uns).Value
			}
		}
	}
	f.Close()
	fmt.Printf("You have %.8f BTC in %d unspent outputs. %d inputs are multisig type\n",
		float64(totBtc)/1e8, len(unspentOuts), multisigInputs)
	if showbalance {
		if unknownInputs > 0 {
			fmt.Printf("WARNING: Some inputs (%d) cannot be spent with this password (-v to print them)\n", unknownInputs);
		}
	}
}
