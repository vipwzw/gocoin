package network

import (
	"os"
	"fmt"
	"net"
	"time"
	"sync"
	"sort"
	"errors"
	"strings"
	"encoding/binary"
	"github.com/vipwzw/gocoin/qdb"
	"github.com/vipwzw/gocoin/others/utils"
	"github.com/vipwzw/gocoin/client/common"
)

const (
	ExpirePeerAfter = (3*time.Hour) // https://en.bitcoin.it/wiki/Protocol_specification#addr
)

var (
	PeerDB *qdb.DB
	proxyPeer *onePeer // when this is not nil we should only connect to this single node
	peerdb_mutex sync.Mutex
)

type onePeer struct {
	*utils.OnePeer
}


func NewEmptyPeer() (p *onePeer) {
	p = new(onePeer)
	p.OnePeer = new(utils.OnePeer)
	return
}

func NewPeer(v []byte) (p *onePeer) {
	p = new(onePeer)
	p.OnePeer = utils.NewPeer(v)
	return
}


func NewIncomingPeer(ipstr string) (p *onePeer, e error) {
	x := strings.Index(ipstr, ":")
	if x != -1 {
		ipstr = ipstr[:x] // remove port number
	}
	ip := net.ParseIP(ipstr)
	if ip != nil && len(ip)==16 {
		if common.IsIPBlocked(ip[12:16]) {
			e = errors.New(ipstr+" is blocked")
			return
		}
		p = NewEmptyPeer()
		copy(p.Ip4[:], ip[12:16])
		p.Services = common.Services
		copy(p.Ip6[:], ip[:12])
		p.Port = common.DefaultTcpPort
		if dbp := PeerDB.Get(qdb.KeyType(p.UniqID())); dbp!=nil && NewPeer(dbp).Banned!=0 {
			e = errors.New(p.Ip() + " is banned")
			p = nil
		} else {
			p.Time = uint32(time.Now().Unix())
			p.Save()
		}
	} else {
		e = errors.New("Error parsing IP '"+ipstr+"'")
	}
	return
}


func ExpirePeers() {
	peerdb_mutex.Lock()
	var delcnt uint32
	now := time.Now()
	todel := make([]qdb.KeyType, PeerDB.Count())
	PeerDB.Browse(func(k qdb.KeyType, v []byte) uint32 {
		ptim := binary.LittleEndian.Uint32(v[0:4])
		if now.After(time.Unix(int64(ptim), 0).Add(ExpirePeerAfter)) {
			todel[delcnt] = k // we cannot call Del() from here
			delcnt++
		}
		return 0
	})
	if delcnt > 0 {
		common.CountSafeAdd("PeersExpired", uint64(delcnt))
		for delcnt > 0 {
			delcnt--
			PeerDB.Del(todel[delcnt])
		}
		common.CountSafe("PeerDefragsDone")
		PeerDB.Defrag()
	} else {
		common.CountSafe("PeerDefragsNone")
	}
	peerdb_mutex.Unlock()
}


func (p *onePeer) Save() {
	PeerDB.Put(qdb.KeyType(p.UniqID()), p.Bytes())
}


func (p *onePeer) Ban() {
	p.Banned = uint32(time.Now().Unix())
	p.Save()
}


func (p *onePeer) Alive() {
	prv := int64(p.Time)
	now := time.Now().Unix()
	p.Time = uint32(now)
	if now-prv >= 60 {
		p.Save() // Do not save more often than once per minute
	}
}


func (p *onePeer) Dead() {
	p.Time -= 600 // make it 10 min older
	p.Save()
}


func (p *onePeer) Ip() (string) {
	return fmt.Sprintf("%d.%d.%d.%d:%d", p.Ip4[0], p.Ip4[1], p.Ip4[2], p.Ip4[3], p.Port)
}


func (p *onePeer) String() (s string) {
	s = fmt.Sprintf("%21s", p.Ip())

	now := uint32(time.Now().Unix())
	if p.Banned != 0 {
		s += fmt.Sprintf("  *BAN %3d min ago", (now-p.Banned)/60)
	} else {
		s += fmt.Sprintf("  Seen %3d min ago", (now-p.Time)/60)
	}
	return
}


type manyPeers []*onePeer

func (mp manyPeers) Len() int {
	return len(mp)
}

func (mp manyPeers) Less(i, j int) bool {
	return mp[i].Time > mp[j].Time
}

func (mp manyPeers) Swap(i, j int) {
	mp[i], mp[j] = mp[j], mp[i]
}


// Fetch a given number of best (most recenty seen) peers.
// Set unconnected to true to only get those that we are not connected to.
func GetBestPeers(limit uint, unconnected bool) (res manyPeers) {
	if proxyPeer!=nil {
		if !unconnected || !ConnectionActive(proxyPeer) {
			return manyPeers{proxyPeer}
		}
		return manyPeers{}
	}
	peerdb_mutex.Lock()
	tmp := make(manyPeers, 0)
	PeerDB.Browse(func(k qdb.KeyType, v []byte) uint32 {
		ad := NewPeer(v)
		if ad.Banned==0 && utils.ValidIp4(ad.Ip4[:]) && !common.IsIPBlocked(ad.Ip4[:]) {
			if !unconnected || !ConnectionActive(ad) {
				tmp = append(tmp, ad)
			}
		}
		return 0
	})
	peerdb_mutex.Unlock()
	// Copy the top rows to the result buffer
	if len(tmp)>0 {
		sort.Sort(tmp)
		if uint(len(tmp))<limit {
			limit = uint(len(tmp))
		}
		res = make(manyPeers, limit)
		copy(res, tmp[:limit])
	}
	return
}


func initSeeds(seeds []string, port uint16) {
	for i := range seeds {
		ad, er := net.LookupHost(seeds[i])
		if er == nil {
			for j := range ad {
				ip := net.ParseIP(ad[j])
				if ip != nil && len(ip)==16 {
					p := NewEmptyPeer()
					p.Time = uint32(time.Now().Unix())
					p.Services = 1
					copy(p.Ip6[:], ip[:12])
					copy(p.Ip4[:], ip[12:16])
					p.Port = port
					p.Save()
				}
			}
		} else {
			println("initSeeds LookupHost", seeds[i], "-", er.Error())
		}
	}
}


// shall be called from the main thread
func InitPeers(dir string) {
	PeerDB, _ = qdb.NewDB(dir+"peers3", true)

	if common.CFG.ConnectOnly != "" {
		x := strings.Index(common.CFG.ConnectOnly, ":")
		if x == -1 {
			common.CFG.ConnectOnly = fmt.Sprint(common.CFG.ConnectOnly, ":", common.DefaultTcpPort)
		}
		oa, e := net.ResolveTCPAddr("tcp4", common.CFG.ConnectOnly)
		if e != nil {
			println(e.Error())
			os.Exit(1)
		}
		proxyPeer = NewEmptyPeer()
		proxyPeer.Services = common.Services
		copy(proxyPeer.Ip4[:], oa.IP[0:4])
		proxyPeer.Port = uint16(oa.Port)
		fmt.Printf("Connect to bitcoin network via %d.%d.%d.%d:%d\n",
			oa.IP[0], oa.IP[1], oa.IP[2], oa.IP[3], oa.Port)
	} else {
		go func() {
			if !common.CFG.Testnet {
				initSeeds([]string{"seed21.macoin.org", "seed22.macoin.org", "seed23.macoin.org"}, 10998)
			} else {
				initSeeds([]string{"seed21.macoin.org", "seed22.macoin.org", "seed23.macoin.org"}, 18333)
			}
		}()
	}
}


func ClosePeerDB() {
	if PeerDB!=nil {
		fmt.Println("Closing peer DB")
		PeerDB.Sync()
		PeerDB.Defrag()
		PeerDB.Close()
		PeerDB = nil
	}
}
