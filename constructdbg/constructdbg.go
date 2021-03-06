package constructdbg

import (
	"bufio"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudesheng/ga/bnt"
	"github.com/mudesheng/ga/cbrotli"
	"github.com/mudesheng/ga/constructcf"
	"github.com/mudesheng/ga/cuckoofilter"
	"github.com/mudesheng/ga/utils"
	// "time"

	"github.com/awalterschulze/gographviz"
	"github.com/biogo/biogo/alphabet"
	"github.com/biogo/biogo/io/seqio/fasta"
	"github.com/biogo/biogo/io/seqio/fastq"
	"github.com/biogo/biogo/seq/linear"
	"github.com/jwaldrip/odin/cli"
)

type DBG_MAX_INT uint32 // use marker DBG node ID and DBG edge

type DBGNode struct {
	ID              DBG_MAX_INT
	EdgeIDIncoming  [bnt.BaseTypeNum]DBG_MAX_INT // the ID of EdgeID inclming
	EdgeIDOutcoming [bnt.BaseTypeNum]DBG_MAX_INT
	Seq             []uint64 // kmer node seq, use 2bit schema
	SubGID          uint8    // Sub graph ID, most allow 2**8 -1 subgraphs
	Flag            uint8    // from low~high, 1:Process, 2:
}

const (
	DIN             uint8 = 1 // note DBGNode and DBGEdge relationship
	DOUT            uint8 = 2 // note DBGNode and DBGEdge relationship
	PLUS, MINUS           = true, false
	NODEMAP_KEY_LEN       = 7
)

type NodeInfoByEdge struct {
	n1, n2 DBGNode
	c1, c2 uint8
	i1, i2 uint
	Flag   bool
	edgeID DBG_MAX_INT
}

func (n *DBGNode) GetProcessFlag() uint8 {
	return n.Flag & 0x1
}

func (n *DBGNode) SetProcessFlag() {
	// n.Flag = n.Flag & 0xFE
	n.Flag |= 0x1
}

func (n *DBGNode) ResetProcessFlag() {
	n.Flag &^= 0x1
}

func (n *DBGNode) GetDeleteFlag() uint8 {
	return n.Flag & 0x2
}

func (n *DBGNode) SetDeleteFlag() {
	// n.Flag = n.Flag & 0xFE
	n.Flag |= 0x2
}

type Unitig struct {
	Ks []byte
	Kq []uint8
}

type Path struct {
	IDArr []DBG_MAX_INT
	Freq  int
}

type DBGEdge struct {
	ID       DBG_MAX_INT
	StartNID DBG_MAX_INT // start node ID
	EndNID   DBG_MAX_INT // end node ID
	CovD     uint16      // Coverage Depth
	Flag     uint8       //
	Utg      Unitig
	PathMat  []Path // read Path matrix
}

/*
func EqualDBG_MAX_INT(path1, path2 []DBG_MAX_INT) bool {
	if len(path1) != len(path2) {
		return false
	}

	for i, v := range path1 {
		if v != path2[i] {
			return false
		}
	}
	return true
} */

func (e *DBGEdge) InsertPathToEdge(path []DBG_MAX_INT, freq int) {

	// check have been added
	added := false
	for i, v := range e.PathMat {
		rp := GetReverseDBG_MAX_INTArr(path)
		if reflect.DeepEqual(v.IDArr, path) || reflect.DeepEqual(v.IDArr, rp) {
			e.PathMat[i].Freq += freq
			added = true
			break
		}
	}
	if added == false {
		var np Path
		np.IDArr = make([]DBG_MAX_INT, len(path))
		copy(np.IDArr, path)
		np.Freq = freq
		e.PathMat = append(e.PathMat, np)
	}
}

/*func InsertPathToEdge(pathMat []Path, path []DBG_MAX_INT, freq int) []Path {

	// check have been added
	added := false
	for i, v := range pathMat {
		if EqualDBG_MAX_INT(v.IDArr, path) {
			pathMat[i].Freq += freq
			added = true
			break
		}
	}
	if added == false {
		var np Path
		np.IDArr = make([]DBG_MAX_INT, len(path))
		copy(np.IDArr, path)
		np.Freq = freq
		pathMat = append(pathMat, np)
	}

	return pathMat
}*/

func (e *DBGEdge) GetProcessFlag() uint8 {
	return e.Flag & 0x1
}

func (e *DBGEdge) GetDeleteFlag() uint8 {
	return e.Flag & 0x2
}

func (e *DBGEdge) GetUniqueFlag() uint8 {
	return e.Flag & 0x4
}

func (e *DBGEdge) GetSemiUniqueFlag() uint8 {
	return e.Flag & 0x8
}

func (e *DBGEdge) GetTwoEdgesCycleFlag() uint8 {
	return e.Flag & 0x10
}

func (e *DBGEdge) SetSemiUniqueFlag() {
	e.Flag = e.Flag | 0x8
}

func (e *DBGEdge) SetTwoEdgesCycleFlag() {
	e.Flag = e.Flag | 0x10
}

func (e *DBGEdge) ResetTwoEdgesCycleFlag() {
	e.Flag = e.Flag & (0xFF - 0x10)
}

func (e *DBGEdge) SetUniqueFlag() {
	e.Flag = e.Flag | 0x4
}

func (e *DBGEdge) ResetUniqueFlag() {
	e.Flag = e.Flag & (0xFF - 0x4)
}

func (e *DBGEdge) ResetSemiUniqueFlag() {
	e.Flag = e.Flag & (0xFF - 0x8)
}

func (e *DBGEdge) SetProcessFlag() {
	e.Flag = e.Flag | 0x1
}

func (e *DBGEdge) ResetProcessFlag() {
	e.Flag = e.Flag & (0xFF - 0x1)
}

func (e *DBGEdge) SetDeleteFlag() {
	e.Flag = e.Flag | 0x2
}

func (e *DBGEdge) GetSeqLen() int {
	return len(e.Utg.Ks)
}

var MIN_KMER_COUNT uint16 = 3
var BACKWARD uint8 = 1
var FORWARD uint8 = 2

//var Kmerlen int = -1

func ReverseDBG_MAX_INTArr(path []DBG_MAX_INT) []DBG_MAX_INT {
	for i := 0; i < len(path)/2; i++ {
		path[i], path[len(path)-1-i] = path[len(path)-1-i], path[i]
	}

	return path
}

func readUniqKmer(brfn string, cs chan constructcf.KmerBntBucket, kmerlen, numCPU int) {
	fp, err := os.Open(brfn)
	if err != nil {
		log.Fatal(err)
	}
	defer fp.Close()
	brfp := cbrotli.NewReaderSize(fp, 1<<25)
	defer brfp.Close()
	buffp := bufio.NewReader(brfp)
	var processNumKmer int
	// var rsb constructcf.ReadSeqBucket
	eof := false
	KBntUint64Num := (kmerlen + bnt.NumBaseInUint64 - 1) / bnt.NumBaseInUint64
	var buck constructcf.KmerBntBucket
	for !eof {
		// var kmer []byte
		b := make([]uint64, KBntUint64Num)
		// n, err := ukbuffp.Read(b)
		err := binary.Read(buffp, binary.LittleEndian, b)
		/*if n < KBntByteNum && err == nil {
			n1, err1 := ukbuffp.Read(b[n:])
			if err1 != nil {
				log.Fatalf("[readUniqKmer] read kmer seq err1: %v\n", err1)
			}
			n += n1
		}*/
		// fmt.Printf("[readUniqKmer]len(b): %v,  b: %v\n", len(b), b)
		if err != nil {
			if err == io.EOF {
				eof = true
				if buck.Count > 0 {
					cs <- buck
				}
				break
			} else {
				log.Fatalf("[readUniqKmer] read kmer seq err: %v\n", err)
			}
			// fmt.Printf("[readUniqKmer]: read %d bytes\n", n)
		}
		if len(b) != KBntUint64Num {
			log.Fatalf("[readUniqKmer] read kmer seq length: %v != KBntByteNum[%v]\n", len(b), KBntUint64Num)
		}
		// if n != KBntByteNum {
		// 	// log.Fatalf("[readUniqKmer] read kmer seq err: n(%d) != KBntByteNum(%d)\n", n, KBntByteNum)
		// }
		var rb constructcf.KmerBnt
		rb.Seq = b
		// if len(rb.Seq) != kmerlen {
		// 	log.Fatalf("[readUniqKmer] len(rb.Seq) != kmerlen\n")
		// } else {
		rb.Len = kmerlen
		// }
		if buck.Count >= constructcf.ReadSeqSize {
			cs <- buck
			var nb constructcf.KmerBntBucket
			buck = nb
		}
		buck.KmerBntBuf[buck.Count] = rb
		buck.Count++
		processNumKmer++
	}

	fmt.Printf("[readUniqKmer] total read kmer number is : %d\n", processNumKmer)
	// send read finish signal
	close(cs)
}

func UniDirectExtend(kb, rkb constructcf.KmerBnt, cf cuckoofilter.CuckooFilter, min_kmer_count uint16, direction uint8) (ec int) {
	kmerlen := cf.Kmerlen
	//var nBnt constructcf.ReadBnt
	//nBnt.Seq = make([]byte, kmerlen)
	if direction == FORWARD {
		//copy(nBnt.Seq[:kmerlen-1], nb.Seq)
		for i := 0; i < bnt.BaseTypeNum; i++ {
			b := uint64(i)
			//nBnt.Seq[kmerlen-1] = b
			ks := constructcf.GetNextKmer(kb, b, kmerlen)
			rs := constructcf.GetPreviousKmer(rkb, uint64(bnt.BntRev[b]), kmerlen)
			if ks.BiggerThan(rs) {
				ks = rs
			}
			if count := cf.GetCountAllowZero(ks.Seq); count >= min_kmer_count {
				ec++
			}
		}
	} else { // direction == BACKWARD
		//copy(nBnt.Seq[1:], nb.Seq)
		for i := 0; i < bnt.BaseTypeNum; i++ {
			b := uint64(i)
			//nBnt.Seq[0] = b
			ks := constructcf.GetPreviousKmer(kb, b, kmerlen)
			rs := constructcf.GetNextKmer(rkb, uint64(bnt.BntRev[b]), kmerlen)
			if ks.BiggerThan(rs) {
				ks = rs
			}
			if count := cf.GetCountAllowZero(ks.Seq); count >= min_kmer_count {
				ec++
			}
		}
	}

	return
}

func ExtendNodeKmer(nkb, rkb constructcf.KmerBnt, cf cuckoofilter.CuckooFilter, min_kmer_count uint16) (nd DBGNode) {
	kmerlen := cf.Kmerlen
	var kb2, rb2 constructcf.KmerBnt
	kb2.Seq = make([]uint64, (kmerlen+bnt.NumBaseInUint64-1)/bnt.NumBaseInUint64)
	rb2.Seq = make([]uint64, (kmerlen+bnt.NumBaseInUint64-1)/bnt.NumBaseInUint64)
	//var nBnt constructcf.ReadBnt
	//nBnt.Seq = make([]byte, kmerlen+1)
	//nd.Seq = constructcf.GetReadBntKmer(nodeBnt, 0, kmerlen-1).Seq
	//copy(nBnt.Seq[1:], nodeBnt.Seq)
	//rkb := constructcf.ReverseComplet(nkb)
	nd.Seq = nkb.Seq
	for i := 0; i < bnt.BaseTypeNum; i++ {
		bi := uint64(i)
		//nBnt.Seq[0] = bi
		{
			//ks := constructcf.GetPreviousKmer(nkb, bi, kmerlen)
			//rs := constructcf.GetNextKmer(rkb, uint64(bnt.BntRev[bi]), kmerlen)
			kb2 = constructcf.NoAllocGetPreviousKmer(nkb, kb2, bi, kmerlen)
			rb2 = constructcf.NoAllocGetNextKmer(rkb, rb2, uint64(bnt.BntRev[bi]), kmerlen)
			min := kb2
			if kb2.BiggerThan(rb2) {
				min = rb2
			}
			count := cf.GetCountAllowZero(min.Seq)
			if count >= min_kmer_count {
				//fmt.Printf("[ExtendNodeKmer]count: %v\n", count)
				//var nb constructcf.ReadBnt
				//nb.Seq = append(nb.Seq, nBnt.Seq[:kmerlen-1]...)
				//nb.Length = len(nb.Seq)
				/*if ec := UniDirectExtend(ks, rs, cf, min_kmer_count, BACKWARD); ec > 0 {
					if direction == FORWARD {
						baseBnt = uint8(bi)
						baseCount = count
					}
					//fmt.Printf("[ExtendNodeKmer] BACKWARD bi: %v, ks.Seq: %v\n", bi, ks.Seq)
				}*/
				nd.EdgeIDIncoming[bi] = 1
			}
		}

		{
			//nBnt.Seq[cf.Kmerlen] = bi
			kb2 = constructcf.NoAllocGetNextKmer(nkb, kb2, bi, kmerlen)
			rb2 = constructcf.NoAllocGetPreviousKmer(rkb, rb2, uint64(bnt.BntRev[bi]), kmerlen)

			min := kb2
			if kb2.BiggerThan(rb2) {
				min = rb2
			}
			count := cf.GetCountAllowZero(min.Seq)
			if count >= min_kmer_count {
				//fmt.Printf("[ExtendNodeKmer]count: %v\n", count)
				//var nb constructcf.ReadBnt
				//nb.Seq = append(nb.Seq, nBnt.Seq[2:]...)
				//nb.Length = len(nb.Seq)
				/*if ec := UniDirectExtend(ks, rs, cf, min_kmer_count, FORWARD); ec > 0 {
					if direction == BACKWARD {
						baseBnt = uint8(i)
						baseCount = count
					}
					//fmt.Printf("[ExtendNodeKmer] FORWARD bi: %v, ks.Seq: %v\n", bi, ks.Seq)
				}*/
				nd.EdgeIDOutcoming[bi] = 1
			}
		}
	}

	return
}

func ChangeEdgeIDComing(nd DBGNode) DBGNode {
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if nd.EdgeIDIncoming[i] == math.MaxUint32 {
			nd.EdgeIDIncoming[i] = 1
		}
		if nd.EdgeIDOutcoming[i] == math.MaxUint32 {
			nd.EdgeIDOutcoming[i] = 1
		}
	}
	return nd
}

func GetMinDBGNode(nd DBGNode, kmerlen int) (minN DBGNode) {
	var nkb constructcf.KmerBnt
	nkb.Len = kmerlen - 1
	nkb.Seq = nd.Seq
	rkb := constructcf.ReverseComplet(nkb)
	//fmt.Printf("[GetMinDBGNode] node: %v\nRC node: %v\n", nodeBnt, rnode)
	if nkb.BiggerThan(rkb) {
		minN.Seq = rkb.Seq
		for i := 0; i < bnt.BaseTypeNum; i++ {
			minN.EdgeIDIncoming[i] = nd.EdgeIDOutcoming[bnt.BaseTypeNum-1-i]
			minN.EdgeIDOutcoming[i] = nd.EdgeIDIncoming[bnt.BaseTypeNum-1-i]
		}
	} else {
		minN = nd
	}
	return
}

func paraLookupComplexNode(cs chan constructcf.KmerBntBucket, wc chan DBGNode, cf cuckoofilter.CuckooFilter) {
	// var wrsb constructcf.ReadSeqBucket
	for {
		kbBucket, ok := <-cs
		if !ok {
			var nd DBGNode
			wc <- nd
			break
		}
		// if rsb.Count < constructcf.ReadSeqSize {
		// 	fmt.Printf("rsb.ReadBuf length is : %d\n", len(rsb.ReadBuf))
		// }
		// if found kmer count is 1 , this kmer will be ignore, and skip this branch
		for j := 0; j < kbBucket.Count; j++ {
			kb := kbBucket.KmerBntBuf[j]
			//fmt.Printf("[paraLookupComplexNode] kb : %v\n", kb)
			{ // check fisrt node of kmer
				nkb, _ := constructcf.DeleteLastBaseKmer(kb)
				//fmt.Printf("[paraLookupComplexNode] nkb : %v\n", nkb)
				rkb := constructcf.ReverseComplet(nkb)
				//fmt.Printf("[paraLookupComplexNode] rkb : %v\n", rkb)
				//extkb := constructcf.ExtendKmerBnt2Byte(kb)
				//extnkb := constructcf.ExtendKmerBnt2Byte(nkb)
				//extrkb := constructcf.ExtendKmerBnt2Byte(rkb)
				//fmt.Printf("[paraLookupComplexNode]first kb: %v\n\tbase: %v,nkb: %v\n\trkb: %v\n", constructcf.ExtendKmerBnt2Byte(kb), base, constructcf.ExtendKmerBnt2Byte(nkb), constructcf.ExtendKmerBnt2Byte(rkb))
				//nodeBnt.Length = len(nodeBnt.Seq)
				nd := ExtendNodeKmer(nkb, rkb, cf, MIN_KMER_COUNT)
				var leftcount, rightcount int
				for i := 0; i < bnt.BaseTypeNum; i++ {
					if nd.EdgeIDIncoming[i] == 1 {
						leftcount++
					}
					if nd.EdgeIDOutcoming[i] == 1 {
						rightcount++
					}
				}
				if leftcount > 1 || rightcount > 1 {
					var wd DBGNode
					wd.Seq = make([]uint64, len(nkb.Seq))
					if nkb.BiggerThan(rkb) {
						nkb = rkb
						for i := 0; i < bnt.BaseTypeNum; i++ {
							wd.EdgeIDIncoming[i] = nd.EdgeIDOutcoming[bnt.BaseTypeNum-1-i]
							wd.EdgeIDOutcoming[i] = nd.EdgeIDIncoming[bnt.BaseTypeNum-1-i]
						}
					} else {
						wd.EdgeIDIncoming = nd.EdgeIDIncoming
						wd.EdgeIDOutcoming = nd.EdgeIDOutcoming
					}
					copy(wd.Seq, nkb.Seq)
					//tn := GetMinDBGNode(nd, cf.Kmerlen)
					//fmt.Printf("[paraLookupComplexNode] nd: %v\n", nd)
					//fmt.Printf("[paraLookupComplexNode] node: %v\n", wd)
					wc <- wd
				}
			}

			{ // check second node of kmer
				nkb, _ := constructcf.DeleteFirstBaseKmer(kb)
				//fmt.Printf("[paraLookupComplexNode]second kb: %v\nbase: %v,nkb: %v\n", constructcf.ExtendKmerBnt2Byte(kb), base, constructcf.ExtendKmerBnt2Byte(nkb))
				rkb := constructcf.ReverseComplet(nkb)
				nd := ExtendNodeKmer(nkb, rkb, cf, MIN_KMER_COUNT)
				var leftcount, rightcount int
				for i := 0; i < bnt.BaseTypeNum; i++ {
					if nd.EdgeIDIncoming[i] == 1 {
						leftcount++
					}
					if nd.EdgeIDOutcoming[i] == 1 {
						rightcount++
					}
				}
				if leftcount > 1 || rightcount > 1 {
					var wd DBGNode
					wd.Seq = make([]uint64, len(nkb.Seq))
					if nkb.BiggerThan(rkb) {
						nkb = rkb
						for i := 0; i < bnt.BaseTypeNum; i++ {
							wd.EdgeIDIncoming[i] = nd.EdgeIDOutcoming[bnt.BaseTypeNum-1-i]
							wd.EdgeIDOutcoming[i] = nd.EdgeIDIncoming[bnt.BaseTypeNum-1-i]
						}
					} else {
						wd.EdgeIDIncoming = nd.EdgeIDIncoming
						wd.EdgeIDOutcoming = nd.EdgeIDOutcoming
					}
					copy(wd.Seq, nkb.Seq)
					//fmt.Printf("[paraLookupComplexNode] node: %v\n", wd)
					wc <- nd
				}
			}
		}
	}
}

func constructNodeMap(complexKmerfn string, nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, NBntUint64Len int) DBG_MAX_INT {
	nodeID := DBG_MAX_INT(2)
	ckfp, err := os.Open(complexKmerfn)
	if err != nil {
		log.Fatalf("[constructNodeMap] open %s file failed: %v\n", complexKmerfn, err)
	}
	defer ckfp.Close()
	//brfp := cbrotli.NewReaderSize(ckfp, 1<<25)
	//defer brfp.Close()
	buffp := bufio.NewReader(ckfp)
	eof := false
	readnodeNum := 0
	for !eof {
		var node DBGNode
		node.Seq = make([]uint64, NBntUint64Len)
		err := binary.Read(buffp, binary.LittleEndian, node.Seq)
		if err != nil {
			if err == io.EOF {
				eof = true
				break
			} else {
				log.Fatalf("[constructNodeMap] err: %v\n", err)
			}
		}
		if err := binary.Read(buffp, binary.LittleEndian, &node.EdgeIDIncoming); err != nil {
			log.Fatalf("[constructNodeMap] read file: %s err: %v\n", complexKmerfn, err)
		}
		if err := binary.Read(buffp, binary.LittleEndian, &node.EdgeIDOutcoming); err != nil {
			log.Fatalf("[constructNodeMap] read file: %s err: %v\n", complexKmerfn, err)
		}
		// fmt.Fprintf(os.Stderr, "[constructNodeMap] node: %v\n", node)
		// n, err := ckfp.Read(b)
		// if n != NBntByteLen {
		// 	log.Fatalf("[constructNodeMap] read node seq err: n(%d) != NBntByteLen(%d\n)", n, NBntByteLen)
		// }
		readnodeNum++
		node.ID = nodeID
		var key [NODEMAP_KEY_LEN]uint64
		copy(key[:], node.Seq)
		//fmt.Printf("[constructNodeMap] key: %v\n\tnode: %v\n", key, node)
		if _, ok := nodeMap[key]; ok == false {
			nodeMap[key] = node
			nodeID++
			//fmt.Printf("[constructNodeMap] node: %v\n", node)
		} else {
			//fmt.Printf("[constructNodeMap] repeat node: %v\n", node)
		}
	}

	fmt.Printf("[constructNodeMap] read node number is : %d\n", readnodeNum)

	return nodeID
}

func AddNodeToNodeMap(node DBGNode, nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, nodeID DBG_MAX_INT) DBG_MAX_INT {
	/*if node.Flag != 1 {
		log.Fatalf("[AddNodeToNodeMap] found node.Flag: %v != 1\n", node.Flag)
	}*/
	var key [NODEMAP_KEY_LEN]uint64
	copy(key[:], node.Seq)
	if _, ok := nodeMap[key]; ok == false {
		node.ID = nodeID
		nodeMap[key] = node
		nodeID++
	} else {
		log.Fatalf("[AddNodeToNodeMap] node: %v has been exist in the nodeMap\n", node)
	}

	return nodeID
}

func AddNewDBGNode(narr []DBGNode, anc <-chan DBGNode, finish <-chan bool) {
loop:
	for {
		select {
		case nd := <-anc:
			narr = append(narr, nd)
		case <-finish:
			for nd := range anc {
				narr = append(narr, nd)
			}
			break loop
		}
	}
}

func CollectAddedDBGNode(anc chan DBGNode, nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, nc chan<- DBGNode, nodeID *DBG_MAX_INT, readNodeMapFinishedC <-chan int) {
	var narr []DBGNode
	var addedNum int
loop:
	for {
		select {
		case nd := <-anc:
			narr = append(narr, nd)
		case <-readNodeMapFinishedC:
			break loop
		}
	}

	// add new DBGnode to the narr
	fTag := make(chan bool)
	go AddNewDBGNode(narr, anc, fTag)

	//runs := 4
	for len(narr) > 0 || len(nc) > 0 {
		for j := 0; j < len(narr); j++ {

			var key [NODEMAP_KEY_LEN]uint64
			copy(key[:], narr[j].Seq)
			narr[j].ID = *nodeID
			*nodeID++
			muRW.Lock()
			nodeMap[key] = narr[j]
			muRW.Unlock()
			nc <- narr[j]
			addedNum++
		}
		time.Sleep(time.Second)
	}

	fTag <- true
	//totalNodeNum <- nodeID
	close(anc)
	close(nc)
	fmt.Printf("[CollectAddedDBGNode] added node number is: %d, total node number is: %d\n", addedNum, *nodeID)
}

// ChangeNodeMap add new Node to the nodeMap and check node edge has been output
/*func ChangeNodeMap(nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, anc chan<- DBGNode, finishedC <-chan int, nIEC <-chan NodeInfoByEdge, flagNIEC chan<- NodeInfoByEdge, Kmerlen int, nodeID DBG_MAX_INT) (nID DBG_MAX_INT, edgeID DBG_MAX_INT) {
	oldNodeID := nodeID
	edgeID = DBG_MAX_INT(2)
loop:
	for {
		select {
		case <-finishedC:
			break loop
		case nie := <-nIEC:
			var key [NODEMAP_KEY_LEN]uint64
			copy(key[:], nie.n1.Seq)
			v1 := nodeMap[key]
			if nie.c1 == DIN {
				if v1.EdgeIDIncoming[nie.i1] == 1 {
					v1.EdgeIDIncoming[nie.i1] = edgeID
				} else {
					nie.Flag = false
					fmt.Printf("[ChangeNodeMap] add edge: start node ID: %v, end node ID: %v, same as edgeID: %v\n", nie.n1.ID, nie.n2.ID, v1.EdgeIDIncoming[nie.i1])
					flagNIEC <- nie
					continue loop
				}
			} else {
				if v1.EdgeIDOutcoming[nie.i1] == 1 {
					v1.EdgeIDOutcoming[nie.i1] = edgeID
				} else {
					nie.Flag = false
					fmt.Printf("[ChangeNodeMap] add edge: start node ID: %v, end node ID: %v, same as edgeID: %v\n", nie.n1.ID, nie.n2.ID, v1.EdgeIDOutcoming[nie.i1])
					flagNIEC <- nie
					continue loop
				}
			}
			nie.edgeID = edgeID
			nie.n1 = v1
			nie.Flag = true
			copy(key[:], v1.Seq)
			nodeMap[key] = v1
			nd := nie.n2
			if len(nd.Seq) > 0 {
				tn := GetMinDBGNode(nd, Kmerlen)
				copy(key[:], tn.Seq)
				v2, ok := nodeMap[key]
				if !ok { // this is new node
					v2 = tn
				}
				if reflect.DeepEqual(v2.Seq, nd.Seq) {
					if nie.c2 == DIN {
						v2.EdgeIDIncoming[nie.i2] = edgeID
					} else {
						v2.EdgeIDOutcoming[nie.i2] = edgeID
					}
				} else {
					b := bnt.BntRev[nie.i2]
					if nie.c2 == DIN {
						v2.EdgeIDOutcoming[b] = edgeID
					} else {
						v2.EdgeIDIncoming[b] = edgeID
					}
				}
				copy(key[:], v2.Seq)
				if ok {
					nodeMap[key] = v2
					nie.n2 = v2
				} else {
					v2.Flag = 1
					nodeID = AddNodeToNodeMap(v2, nodeMap, nodeID)
					//fmt.Printf("[ChangeNodeMap] v2: %v\nnodeID: %v\n", v2, nodeID-1)
					t := nodeMap[key]
					nie.n2 = t
					anc <- t
				}
			}
			edgeID++
			flagNIEC <- nie
		}
	}

	fmt.Printf("[ChangeNodeMap] added nodes number is : %d\n", nodeID-oldNodeID)
	nID = nodeID
	return
}*/

var muRW sync.RWMutex

// ReadDBGNodeToChan  read DBG nodeMap and simultaneously add new node to the nodeMap
func ReadDBGNodeToChan(nodeArr []DBGNode, nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, nc chan<- DBGNode, readNodeMapFinished chan<- int) {
	for _, value := range nodeArr {
		if len(value.Seq) > 0 && value.Flag == 0 {
			var key [NODEMAP_KEY_LEN]uint64
			copy(key[:], value.Seq)
			muRW.RLock()
			value = nodeMap[key]
			muRW.RUnlock()
			nc <- value
		}
		//value.Flag = uint8(1)
		//nodeMap[string(value.Seq)] = value
		//}
	}

	// notice Function ChangeNodeMap() has finished read nodeMap
	time.Sleep(time.Second)
	readNodeMapFinished <- 1
	close(readNodeMapFinished)
}

func ReverseByteArr(ba []byte) {
	lba := len(ba)
	for i := 0; i < lba/2; i++ {
		ba[i], ba[lba-1-i] = ba[lba-1-i], ba[i]
	}
}

func GetReverseByteArr(ba []byte) (na []byte) {
	na = make([]byte, len(ba))
	for i := 0; i < len(ba); i++ {
		na[len(ba)-1-i] = ba[i]
	}
	return
}

func GetCompByteArr(seq []byte) (cseq []byte) {
	cseq = make([]byte, len(seq))
	for i, b := range seq {
		cseq[i] = bnt.BntRev[b]
	}
	return
}

func ReverseCompByteArr(seq []byte) {
	ls := len(seq)
	for i := 0; i < ls/2; i++ {
		seq[i], seq[ls-1-i] = bnt.BntRev[seq[ls-1-i]], bnt.BntRev[seq[i]]
	}
	if ls%2 != 0 {
		seq[ls/2] = bnt.BntRev[seq[ls/2]]
	}
}

func GetReverseCompByteArr(seq []byte) []byte {
	sl := len(seq)
	rv := make([]byte, sl)
	for i := 0; i < len(rv); i++ {
		rv[i] = bnt.BntRev[seq[sl-1-i]]
	}

	return rv
}

func ReverseUint8Arr(ua []uint8) {
	lua := len(ua)
	for i := 0; i < lua/2; i++ {
		ua[i], ua[lua-1-i] = ua[lua-1-i], ua[i]
	}
}

func GetEdges(cf cuckoofilter.CuckooFilter, kb, rkb constructcf.KmerBnt, count uint8, direction uint8, MIN_KMER_COUNT uint16) (edge DBGEdge, nd DBGNode) {
	var kb2, rb2 constructcf.KmerBnt
	kb2.Seq = make([]uint64, (cf.Kmerlen-1+bnt.NumBaseInUint64-1)/bnt.NumBaseInUint64)
	rb2.Seq = make([]uint64, (cf.Kmerlen-1+bnt.NumBaseInUint64-1)/bnt.NumBaseInUint64)
	//tb.Seq = make([]uint64, (kmerlen+bnt.NumBaseInUint64-1)/bnt.NumBaseInUint64)
	seq := constructcf.ExtendKmerBnt2Byte(kb)
	if direction == FORWARD {
		edge.Utg.Ks = append(edge.Utg.Ks, seq...)
		edge.Utg.Kq = make([]uint8, cf.Kmerlen-1)
		edge.Utg.Kq = append(edge.Utg.Kq, count)
		//bi := uint64(seq[0])
		nkb, bi := constructcf.DeleteFirstBaseKmer(kb)
		nr, _ := constructcf.DeleteLastBaseKmer(rkb)
		for {
			node := ExtendNodeKmer(nkb, nr, cf, MIN_KMER_COUNT)
			var leftcount, rightcount int
			var baseBnt byte
			for i := 0; i < bnt.BaseTypeNum; i++ {
				if node.EdgeIDIncoming[i] == 1 {
					leftcount++
				}
				if node.EdgeIDOutcoming[i] == 1 {
					baseBnt = uint8(i)
					rightcount++
				}
			}
			if leftcount == 1 && rightcount == 1 {
				edge.Utg.Ks = append(edge.Utg.Ks, baseBnt)
				edge.Utg.Kq = append(edge.Utg.Kq, uint8(MIN_KMER_COUNT))
				//nkb = constructcf.GetNextKmer(nkb, uint64(baseBnt), cf.Kmerlen-1)
				kb2 = constructcf.NoAllocGetNextKmer(nkb, kb2, uint64(baseBnt), cf.Kmerlen-1)
				rb2 = constructcf.NoAllocGetPreviousKmer(nr, rb2, uint64(bnt.BntRev[baseBnt]), cf.Kmerlen-1)
				nkb, kb2 = kb2, nkb
				nr, rb2 = rb2, nr
				bi = uint64(edge.Utg.Ks[len(edge.Utg.Ks)-cf.Kmerlen])
			} else {
				if leftcount > 1 || rightcount > 1 {
					nd = node
					nd.EdgeIDIncoming[bi] = math.MaxUint32
				}
				break
			}
		}
	} else {
		ReverseByteArr(seq)
		edge.Utg.Ks = append(edge.Utg.Ks, seq...)
		edge.Utg.Kq = make([]uint8, cf.Kmerlen-1)
		edge.Utg.Kq = append(edge.Utg.Kq, count)
		nkb, bi := constructcf.DeleteLastBaseKmer(kb)
		nr, _ := constructcf.DeleteFirstBaseKmer(rkb)
		for {
			node := ExtendNodeKmer(nkb, nr, cf, MIN_KMER_COUNT)
			var leftcount, rightcount int
			var baseBnt byte
			for i := 0; i < bnt.BaseTypeNum; i++ {
				if node.EdgeIDIncoming[i] == 1 {
					baseBnt = uint8(i)
					leftcount++
				}
				if node.EdgeIDOutcoming[i] == 1 {
					rightcount++
				}
			}
			if leftcount == 1 && rightcount == 1 {
				edge.Utg.Ks = append(edge.Utg.Ks, baseBnt)
				edge.Utg.Kq = append(edge.Utg.Kq, uint8(MIN_KMER_COUNT))
				//nkb = constructcf.GetPreviousKmer(nkb, uint64(baseBnt), cf.Kmerlen-1)
				//nr = constructcf.GetNextKmer(nr, uint64(bnt.BntRev[baseBnt]), cf.Kmerlen-1)
				kb2 = constructcf.NoAllocGetPreviousKmer(nkb, kb2, uint64(baseBnt), cf.Kmerlen-1)
				rb2 = constructcf.NoAllocGetNextKmer(nr, rb2, uint64(bnt.BntRev[baseBnt]), cf.Kmerlen-1)
				nkb, kb2 = kb2, nkb
				nr, rb2 = rb2, nr
				bi = uint64(edge.Utg.Ks[len(edge.Utg.Ks)-cf.Kmerlen])
			} else {
				if leftcount > 1 || rightcount > 1 {
					nd = node
					nd.EdgeIDOutcoming[bi] = math.MaxUint32
				}
				// Reverse the seq and quality count
				ReverseByteArr(edge.Utg.Ks)
				ReverseUint8Arr(edge.Utg.Kq)
				break
			}
		}
	}

	return
}

//var mu sync.Mutex
//var mapRWMu sync.RWMutex

type EdgeNode struct {
	Edge         DBGEdge
	NodeS, NodeE DBGNode // NodeS note Start Node, NodeE note End Node
}

func paraGenerateDBGEdges(nc <-chan DBGNode, cf cuckoofilter.CuckooFilter, wc chan<- EdgeNode) {
	for {
		node, ok := <-nc
		if !ok {
			var en EdgeNode
			wc <- en
			break
		}
		// read edge seq from cuckoofilter
		var kb constructcf.KmerBnt
		kb.Seq = make([]uint64, len(node.Seq))
		copy(kb.Seq, node.Seq)
		kb.Len = cf.Kmerlen - 1
		rkb := constructcf.ReverseComplet(kb)
		//extRSeq := constructcf.ExtendKmerBnt2Byte(kb)
		// leftcount, rightcount, _, _ := ExtendNodeKmer(extRBnt, cf, MIN_KMER_COUNT, FORWARD)
		//fmt.Printf("[paraGenerateDBGEdges] node: %v\n", node)
		for i := uint(0); i < bnt.BaseTypeNum; i++ {
			bi := uint64(i)
			if node.EdgeIDIncoming[i] == 1 {
				ks := constructcf.GetPreviousKmer(kb, bi, cf.Kmerlen)
				rs := constructcf.GetNextKmer(rkb, uint64(bnt.BntRev[bi]), cf.Kmerlen)
				min := ks
				if ks.BiggerThan(rs) {
					min = rs
				}
				count := cf.GetCountAllowZero(min.Seq)
				if count < MIN_KMER_COUNT {
					log.Fatalf("[paraGenerateDBGEdges] found count[%v]: < [%v], node: %v", count, MIN_KMER_COUNT, node)
				}
				// get edge sequence
				edge, nd := GetEdges(cf, ks, rs, uint8(count), BACKWARD, MIN_KMER_COUNT)
				//fmt.Printf("[paraGenerateDBGEdges]Incoming i:%v, edge: %v\n\tnd: %v\n", i, edge, nd)
				//writedEdge := false
				if len(nd.Seq) > 0 || len(edge.Utg.Ks) > 2*cf.Kmerlen {
					edge.EndNID = node.ID
					var en EdgeNode
					en.NodeE.Seq = constructcf.GetReadBntKmer(edge.Utg.Ks, len(edge.Utg.Ks)-(cf.Kmerlen-1), cf.Kmerlen-1).Seq
					en.NodeE.EdgeIDIncoming[edge.Utg.Ks[len(edge.Utg.Ks)-cf.Kmerlen]] = math.MaxUint32
					en.Edge = edge
					if len(nd.Seq) > 0 {
						en.NodeS.Seq = constructcf.GetReadBntKmer(edge.Utg.Ks, 0, cf.Kmerlen-1).Seq
						en.NodeS.EdgeIDOutcoming[edge.Utg.Ks[cf.Kmerlen-1]] = math.MaxUint32
					}
					wc <- en
				}
			}

			if node.EdgeIDOutcoming[i] == 1 {
				ks := constructcf.GetNextKmer(kb, bi, cf.Kmerlen)
				rs := constructcf.GetPreviousKmer(rkb, uint64(bnt.BntRev[bi]), cf.Kmerlen)
				min := ks
				if ks.BiggerThan(rs) {
					min = rs
				}
				count := cf.GetCountAllowZero(min.Seq)
				if count < MIN_KMER_COUNT {
					log.Fatalf("[paraGenerateDBGEdges] found count[%v]: < [%v], node: %v", count, MIN_KMER_COUNT, node)
				}
				edge, nd := GetEdges(cf, ks, rs, uint8(count), FORWARD, MIN_KMER_COUNT)
				//fmt.Printf("[paraGenerateDBGEdges]Outcoming i:%v, edge: %v\n\tnd: %v\n", i, edge, nd)
				// writedEdge := false
				if len(nd.Seq) > 0 || len(edge.Utg.Ks) > 2*cf.Kmerlen {
					edge.StartNID = node.ID
					var en EdgeNode
					en.Edge = edge
					en.NodeS.Seq = constructcf.GetReadBntKmer(edge.Utg.Ks, 0, cf.Kmerlen-1).Seq
					en.NodeS.EdgeIDOutcoming[edge.Utg.Ks[cf.Kmerlen-1]] = math.MaxUint32
					if len(nd.Seq) > 0 {
						en.NodeE.Seq = constructcf.GetReadBntKmer(edge.Utg.Ks, len(edge.Utg.Ks)-(cf.Kmerlen-1), cf.Kmerlen-1).Seq
						en.NodeE.EdgeIDIncoming[edge.Utg.Ks[len(edge.Utg.Ks)-cf.Kmerlen]] = math.MaxUint32
					}
					wc <- en
				}
			}
		}
	}
}

// WritefqRecord write one record of fastq to the file
func WritefqRecord(edgesbuffp io.Writer, ei DBGEdge) {
	fmt.Fprintf(edgesbuffp, "@%d\t%d\t%d\n", ei.ID, ei.StartNID, ei.EndNID)
	// fmt.Fprintf(edgesgzfp, "%s\n", ei.Utg.Ks)
	for i := 0; i < len(ei.Utg.Ks); i++ {
		if ei.Utg.Ks[i] > 3 || ei.Utg.Ks[i] < 0 {
			log.Fatalf("[WriteEdgesToFn] not correct base: %d:%d\n", i, ei.Utg.Ks[i])
		}
		fmt.Fprintf(edgesbuffp, "%c", bnt.BitNtCharUp[ei.Utg.Ks[i]])
	}
	fmt.Fprintf(edgesbuffp, "\n+\n")
	// write quality to the file
	for i := 0; i < len(ei.Utg.Kq); i++ {
		fmt.Fprintf(edgesbuffp, "%c", ei.Utg.Kq[i]+33)
	}
	if len(ei.Utg.Kq) != len(ei.Utg.Ks) {
		log.Fatalf("[WriteEdgesToFn] len(ei.Utg.Kq):%d != len(ei.Utg.Ks):%d\n", len(ei.Utg.Kq), len(ei.Utg.Ks))
	}
	fmt.Fprintf(edgesbuffp, "\n")
	// fmt.Printf("[WriteEdgesToFn] edge: %v\n", ei)
}

// WriteEdgesToFn write edges seq to the file
func WriteEdgesToFn(edgesfn string, wc <-chan EdgeNode, numCPU int, nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, anc chan<- DBGNode, kmerlen int) (edgeID DBG_MAX_INT) {
	//oldNodeID := nodeID
	edgeID = DBG_MAX_INT(2)
	edgesNum := 0
	edgesfp, err := os.Create(edgesfn)
	if err != nil {
		log.Fatalf("[WriteEdgesToFn] Open file %s failed: %v\n", edgesfn, err)
	}
	defer edgesfp.Close()
	edgesbuffp := bufio.NewWriter(edgesfp)

	finishNum := 0
	for {
		en := <-wc
		ei := en.Edge
		if len(ei.Utg.Ks) == 0 {
			if ei.StartNID != 0 || ei.EndNID != 0 {
				log.Fatalf("[WriteEdgesToFn] err edge: %v\n", ei)
			}
			//fmt.Printf("[WriteEdgesToFn]  edge: %v\n", ei)
			finishNum++
			if finishNum == numCPU {
				break
			}
			continue
		}
		//fmt.Printf("[WriteEdgesToFn] edgeID: %v, en: %v\n", edgeID, en)
		//fmt.Printf("[WriteEdgesToFn] edgeID: %v,len(en.Edge.Utg.Ks): %v,  en.NodeS: %v, en.NodeE: %v\n", edgeID, len(en.Edge.Utg.Ks), en.NodeS, en.NodeE)
		// set edge's node info
		{
			//muRW.Lock()
			var keyS, keyE [NODEMAP_KEY_LEN]uint64
			var vS, vE, tnS, tnE DBGNode
			var okS, okE bool
			if len(en.NodeS.Seq) > 0 {
				tnS = GetMinDBGNode(en.NodeS, kmerlen)
				copy(keyS[:], tnS.Seq)
				muRW.RLock()
				vS, okS = nodeMap[keyS]
				muRW.RUnlock()
				if !okS {
					tnS = ChangeEdgeIDComing(tnS)
					anc <- tnS
				}

				//test code
				/*{
					var rb constructcf.KmerBnt
					rb.Seq = en.NodeS.Seq
					rb.Len = kmerlen - 1
					//revRb := constructcf.GetReadBntKmer(en.Edge.Utg.Ks[:kmerlen-1], 0, kmerlen)
					extNode := constructcf.ExtendKmerBnt2Byte(rb)
					fmt.Printf("[WriteEdgesToFn]base: %v, extNodeS: %v\n", en.Edge.Utg.Ks[kmerlen-1], extNode)
				}*/
			}

			if len(en.NodeE.Seq) > 0 {
				tnE = GetMinDBGNode(en.NodeE, kmerlen)
				copy(keyE[:], tnE.Seq)
				muRW.RLock()
				vE, okE = nodeMap[keyE]
				muRW.RUnlock()
				if !okE {
					tnE = ChangeEdgeIDComing(tnE)
					anc <- tnE
				}

				//test code
				/*{
					var rb constructcf.KmerBnt
					rb.Seq = en.NodeE.Seq
					rb.Len = kmerlen - 1
					extNode := constructcf.ExtendKmerBnt2Byte(rb)
					fmt.Printf("[WriteEdgesToFn]base: %v, extNodeE: %v\n", en.Edge.Utg.Ks[len(en.Edge.Utg.Ks)-kmerlen], extNode)
				}*/
			}
			if okS == false || okE == false {
				//fmt.Printf("[WriteEdgesToFn] okS: %v, okE: %v\n", okS, okE)
				continue
			}
			{ // change nodeMap nodeS EdgeIDComing
				hasWrite := false
				if reflect.DeepEqual(vS.Seq, en.NodeS.Seq) {
					for j := 0; j < bnt.BaseTypeNum; j++ {
						if en.NodeS.EdgeIDOutcoming[j] == math.MaxUint32 {
							if vS.EdgeIDOutcoming[j] == 1 || vS.EdgeIDOutcoming[j] == math.MaxUint32 {
								vS.EdgeIDOutcoming[j] = edgeID
							} else {
								//fmt.Printf("[WriteEdgesToFn] repeat edge, nodeMap edgeID: %v,v: %v\n\tcurrent edge: %v\n", v.EdgeIDOutcoming[j], v, ei)
								//fmt.Fprintf(os.Stderr, ">%d\trepeat edgeID: %d\n%s\n", edgeID, v.EdgeIDOutcoming[j], Transform2Char(ei.Utg.Ks))
								hasWrite = true
							}
							break
						}
					}
				} else {
					for j := 0; j < bnt.BaseTypeNum; j++ {
						if en.NodeS.EdgeIDOutcoming[j] == math.MaxUint32 {
							if vS.EdgeIDIncoming[bnt.BaseTypeNum-1-j] == 1 || vS.EdgeIDIncoming[bnt.BaseTypeNum-1-j] == math.MaxUint32 {
								vS.EdgeIDIncoming[bnt.BaseTypeNum-1-j] = edgeID
							} else {
								//fmt.Printf("[WriteEdgesToFn] repeat edge, nodeMap edgeID: %v,v: %v\n\tcurrent edge: %v\n", v.EdgeIDIncoming[bnt.BaseTypeNum-1-j], v, ei)
								//fmt.Fprintf(os.Stderr, ">%d\trepeat edgeID: %d\n%s\n", edgeID, v.EdgeIDIncoming[bnt.BaseTypeNum-1-j], Transform2Char(ei.Utg.Ks))
								hasWrite = true
							}
							break
						}
					}
				}
				if hasWrite {
					//fmt.Printf("[WriteEdgesToFn] hasWrite: %v\n", hasWrite)
					continue
				}

				muRW.Lock()
				nodeMap[keyS] = vS
				muRW.Unlock()
				ei.StartNID = vS.ID
			}

			{ // change nodeMap nodeE EdgeIDComing
				//hasWrite := false
				// if self cycle edge, vS == vE, need reget vE value
				muRW.RLock()
				vE, okE = nodeMap[keyE]
				muRW.RUnlock()
				if reflect.DeepEqual(vE.Seq, en.NodeE.Seq) {
					for j := 0; j < bnt.BaseTypeNum; j++ {
						if en.NodeE.EdgeIDIncoming[j] == math.MaxUint32 {
							if vE.EdgeIDIncoming[j] == 1 || vE.EdgeIDIncoming[j] == math.MaxUint32 {
								vE.EdgeIDIncoming[j] = edgeID
							} else {
								//hasWrite = true
								log.Fatalf("[WriteEdgesToFn] corrupt with has writed edge ID: %v,v: %v\n\tcurrent edge: %v\nvS: %v\n", vE.EdgeIDIncoming[j], vE, en.Edge, vS)
							}
							break
						}
					}
				} else {
					for j := 0; j < bnt.BaseTypeNum; j++ {
						if en.NodeE.EdgeIDIncoming[j] == math.MaxUint32 {
							if vE.EdgeIDOutcoming[bnt.BaseTypeNum-1-j] == 1 || vE.EdgeIDOutcoming[bnt.BaseTypeNum-1-j] == math.MaxUint32 {
								vE.EdgeIDOutcoming[bnt.BaseTypeNum-1-j] = edgeID
							} else {
								//hasWrite = true
								log.Fatalf("[WriteEdgesToFn] corrupt with has writed edge ID: %v,v: %v\n\tcurrent edge: %v\nvS: %v\n", vE.EdgeIDOutcoming[bnt.BaseTypeNum-1-j], vE, en.Edge, vS)
							}
							break
						}
					}
				}

				muRW.Lock()
				nodeMap[keyE] = vE
				muRW.Unlock()

				ei.EndNID = vE.ID
			}
			ei.ID = edgeID
			ei.StartNID, ei.EndNID = vS.ID, vE.ID
			edgeID++
			WritefqRecord(edgesbuffp, ei)
			edgesNum++
			//fmt.Printf("[WriteEdgesToFn] vS:%v\n\tvE:%v\n", nodeMap[keyS], nodeMap[keyE])
			//fmt.Printf("[WriteEdgesToFn] the writed edgeID: %v, ei.StartNID: %v, ei.EndNID: %v\n\tei.Utg.Ks: %v\n", edgeID-1, ei.StartNID, ei.EndNID, ei.Utg.Ks)
		}
	}
	edgesbuffp.Flush()

	fmt.Printf("[WriteEdgesToFn] the writed file edges number is %d\n", edgesNum)
	//fmt.Printf("[WriteEdgesToFn] added nodes number is : %d\n", nodeID-oldNodeID)
	//nID = nodeID
	return
}

/*func ProcessAddedNode(cf cuckoofilter.CuckooFilter, nodeMap map[string]DBGNode, newNodeBntArr []constructcf.ReadBnt, wc chan DBGEdge, nodeID DBG_MAX_INT) (addedNodesNum, addedEdgesNum int) {

	InitialLen := len(newNodeBntArr)
	processAdded := 0
	totalProcessNUm := 0
	// for _, item := range newNodeBntArr {
	for i := 0; i < len(newNodeBntArr); i++ {
		totalProcessNUm++
		var node DBGNode
		node.ID = nodeID
		node.Seq = newNodeBntArr[i].Seq
		if _, ok := nodeMap[string(node.Seq)]; ok == false {
			nodeMap[string(node.Seq)] = node
			nodeID++
			addedNodesNum++
			// check if need added edges
			var rb constructcf.ReadBnt
			rb.Seq = node.Seq
			rb.Length = cf.Kmerlen - 1
			extRBnt := constructcf.ExtendReadBnt2Byte(rb)
			for i := 0; i < bnt.BaseTypeNum; i++ {
				bi := byte(i)
				if node.EdgeIDIncoming[i] == 0 {
					var nBnt constructcf.ReadBnt
					nBnt.Seq = append(nBnt.Seq, bi)
					nBnt.Seq = append(nBnt.Seq, extRBnt.Seq...)
					nBnt.Length = len(nBnt.Seq)
					ks := constructcf.GetReadBntKmer(nBnt, 0, cf.Kmerlen)
					rs := constructcf.ReverseComplet(ks)
					if ks.BiggerThan(rs) {
						ks, rs = rs, ks
					}
					count := cf.GetCountAllowZero(ks.Seq)
					if count >= MIN_KMER_COUNT {
						// get edge sequence
						edge, isNode := GetEdges(cf, nBnt, uint8(count), BACKWARD, MIN_KMER_COUNT)
						writedEdge := false
						if isNode == true {
							var tBnt constructcf.ReadBnt
							tBnt.Seq = edge.Utg.Ks[:cf.Kmerlen-1]
							tks := constructcf.GetReadBntKmer(tBnt, 0, cf.Kmerlen-1)
							trs := constructcf.ReverseComplet(tks)
							sks := tks
							if sks.BiggerThan((trs)) {
								sks = trs
							}
							if v, ok := nodeMap[string(sks.Seq)]; ok {
								c := edge.Utg.Ks[cf.Kmerlen-1]
								if reflect.DeepEqual(sks, tks) {
									// b := bnt.Base2Bnt[c]
									v.EdgeIDOutcoming[c] = 1
								} else {
									// b := bnt.Base2Bnt[bnt.BitNtRev[c]]
									v.EdgeIDIncoming[bnt.BntRev[c]] = 1
								}
								edge.StartNID = v.ID
								writedEdge = true
								nodeMap[string(sks.Seq)] = v
							} else { // is a new node, add to the newNodeBntArr
								newNodeBntArr = append(newNodeBntArr, sks)
								processAdded++
							}
						} else { // is a tip
							if len(edge.Utg.Ks) > 2*cf.Kmerlen {
								writedEdge = true
							}

						}
						if writedEdge == true {

							node.EdgeIDIncoming[i] = 1
							edge.EndNID = node.ID
							wc <- edge
							// fmt.Printf("[ProcessAddedNode] edge: %v\n", edge)
							addedEdgesNum++
						}
					}
				}

				if node.EdgeIDOutcoming[i] == 0 {
					var nBnt constructcf.ReadBnt
					nBnt.Seq = append(nBnt.Seq, extRBnt.Seq...)
					nBnt.Seq = append(nBnt.Seq, bi)
					nBnt.Length = len(nBnt.Seq)
					ks := constructcf.GetReadBntKmer(nBnt, 0, cf.Kmerlen)
					rs := constructcf.ReverseComplet(ks)
					if ks.BiggerThan(rs) {
						ks, rs = rs, ks
					}
					count := cf.GetCountAllowZero(ks.Seq)
					if count >= MIN_KMER_COUNT {
						edge, isNode := GetEdges(cf, nBnt, uint8(count), FORWARD, MIN_KMER_COUNT)
						writedEdge := false
						if isNode == true {
							var tBnt constructcf.ReadBnt
							tBnt.Seq = edge.Utg.Ks[len(edge.Utg.Ks)-cf.Kmerlen+1:]
							tks := constructcf.GetReadBntKmer(tBnt, 0, cf.Kmerlen-1)
							trs := constructcf.ReverseComplet(tks)
							sks := tks
							if sks.BiggerThan(trs) {
								sks = trs
							}
							if v, ok := nodeMap[string(sks.Seq)]; ok {
								c := edge.Utg.Ks[len(edge.Utg.Ks)-cf.Kmerlen]
								if reflect.DeepEqual(sks, tks) {
									// b := bnt.Base2Bnt[c]
									v.EdgeIDIncoming[c] = 1
								} else {
									// b := bnt.Base2Bnt[bnt.BitNtRev[c]]
									v.EdgeIDOutcoming[bnt.BntRev[c]] = 1
								}
								nodeMap[string(sks.Seq)] = v
								edge.EndNID = v.ID
								writedEdge = true
							} else {
								newNodeBntArr = append(newNodeBntArr, sks)
								processAdded++
							}
						} else { // is a tip
							if len(edge.Utg.Ks) > 2*cf.Kmerlen {
								writedEdge = true
							}
						}
						if writedEdge == true {
							node.EdgeIDOutcoming[i] = 1
							edge.StartNID = node.ID
							wc <- edge
							// fmt.Printf("[ProcessAddedNode] edge: %v\n", edge)
						}
					}
				}
			}
		}
	}

	// add a nil edge to wc, tell have not any more edge need to write
	var edge DBGEdge
	wc <- edge

	fmt.Printf("[ProcessAddedNode] initial newNodeBntArr len: %d, added node number: %d, at last newNodeBntArr len:%d, totalProcessNUm: %d\n", InitialLen, processAdded, len(newNodeBntArr), totalProcessNUm)

	return
} */

/*func cleanEdgeIDInNodeMap(nodeMap map[string]DBGNode) {
	for k, v := range nodeMap {
		for i := 0; i < bnt.BaseTypeNum; i++ {
			v.EdgeIDIncoming[i] = 0
			v.EdgeIDOutcoming[i] = 0
		}
		nodeMap[k] = v
	}
}*/

func GenerateDBGEdges(nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, cf cuckoofilter.CuckooFilter, edgesfn string, numCPU int, nodeID DBG_MAX_INT) (newNodeID DBG_MAX_INT, edgeID DBG_MAX_INT) {
	bufsize := 50
	nc := make(chan DBGNode)
	wc := make(chan EdgeNode, bufsize)
	defer close(wc)
	readNodeMapFinishedC := make(chan int)
	nodeArr := make([]DBGNode, nodeID)
	idx := 0
	for _, value := range nodeMap {
		if len(value.Seq) > 0 && value.Flag == 0 {
			nodeArr[idx] = value
			idx++
		}
	}
	nodeArr = nodeArr[:idx]
	// Read DBGNode to the nc
	go ReadDBGNodeToChan(nodeArr, nodeMap, nc, readNodeMapFinishedC)
	// parallel construct edges from cuckoofilter
	for i := 0; i < numCPU; i++ {
		go paraGenerateDBGEdges(nc, cf, wc)
	}
	// collect added node and pass DBGNode to the nc
	anc := make(chan DBGNode, bufsize)
	//totalNodeNum := make(chan DBG_MAX_INT，1)
	go CollectAddedDBGNode(anc, nodeMap, nc, &nodeID, readNodeMapFinishedC)
	// write edges Seq to the file
	edgeID = WriteEdgesToFn(edgesfn, wc, numCPU, nodeMap, anc, cf.Kmerlen)
	newNodeID = nodeID
	// Change nodeMap monitor function
	//newNodeID, edgeID = ChangeNodeMap(nodeMap, anc, finishedC, nIEC, flagNIEC, cf.Kmerlen, nodeID)

	/* // cache the new added node Bnt info
	var newNodeBntArr []constructcf.ReadBnt
	finishedCount := 0
	for {
		nb := <-newNodeChan
		if nb.Length == 0 {
			finishedCount++
			if finishedCount == numCPU {
				break
			} else {
				continue
			}
		}

		newNodeBntArr = append(newNodeBntArr, nb)
	}

	// process added nodes' edges
	addedNodesNum, addedEdgesNum := ProcessAddedNode(cf, nodeMap, newNodeBntArr, wc, nodeID)
	newNodeID = nodeID + DBG_MAX_INT(addedNodesNum) */

	// clean set edgeID in the DBGNode
	//cleanEdgeIDInNodeMap(nodeMap)
	// fmt.Printf("[GenerateDBGEdges] added nodes number is : %d, added edges number is : %d\n", addedNodesNum, addedEdgesNum)
	return
}

func DBGStatWriter(DBGStatfn string, newNodeID, edgeID DBG_MAX_INT) {
	DBGStatfp, err := os.Create(DBGStatfn)
	if err != nil {
		log.Fatalf("[DBGStatWriter] file %s create error: %v\n", DBGStatfn, err)
	}
	defer DBGStatfp.Close()
	fmt.Fprintf(DBGStatfp, "nodes size:\t%v\n", newNodeID)
	fmt.Fprintf(DBGStatfp, "edges size:\t%v\n", edgeID)
}

func DBGStatReader(DBGStatfn string) (nodesSize, edgesSize DBG_MAX_INT) {
	DBGStatfp, err := os.Open(DBGStatfn)
	if err != nil {
		log.Fatalf("[DBGStatReader] file %s Open error: %v\n", DBGStatfn, err)
	}
	defer DBGStatfp.Close()
	if _, err = fmt.Fscanf(DBGStatfp, "nodes size:\t%v\n", &nodesSize); err != nil {
		log.Fatalf("[DBGStatReader] file: %v, nodes size parse error: %v\n", DBGStatfn, err)
	}
	if _, err = fmt.Fscanf(DBGStatfp, "edges size:\t%v\n", &edgesSize); err != nil {
		log.Fatalf("[DBGStatReader] file: %v, edges size parse error: %v\n", DBGStatfn, err)
	}

	return
}

func DBGInfoWriter(DBGInfofn string, edgesArrSize, nodesArrSize int) {
	DBGInfofp, err := os.Create(DBGInfofn)
	if err != nil {
		log.Fatalf("[DBGInfoWriter] file %s create error: %v\n", DBGInfofn, err)
	}
	defer DBGInfofp.Close()
	fmt.Fprintf(DBGInfofp, "edgesArr size:\t%v\n", edgesArrSize)
	fmt.Fprintf(DBGInfofp, "nodesArr size:\t%v\n", nodesArrSize)
}

func DBGInfoReader(DBGInfofn string) (edgesArrSize, nodesArrSize int) {
	DBGInfofp, err := os.Open(DBGInfofn)
	if err != nil {
		log.Fatalf("[DBGInfoReader] file %s Open error: %v\n", DBGInfofn, err)
	}
	defer DBGInfofp.Close()
	_, err = fmt.Fscanf(DBGInfofp, "edgesArr size:\t%v\n", &edgesArrSize)
	if err != nil {
		log.Fatalf("[edgesStatWriter] edgesArr size parse error: %v\n", err)
	}
	_, err = fmt.Fscanf(DBGInfofp, "nodesArr size:\t%v\n", &nodesArrSize)
	if err != nil {
		log.Fatalf("[edgesStatWriter] nodesArr size parse error: %v\n", err)
	}
	return edgesArrSize, nodesArrSize
}

func EdgesStatWriter(edgesStatfn string, edgesSize int) {
	edgesStatfp, err := os.Create(edgesStatfn)
	if err != nil {
		log.Fatalf("[EdgesStatWriter] file %s create error: %v\n", edgesStatfn, err)
	}
	defer edgesStatfp.Close()
	fmt.Fprintf(edgesStatfp, "edges size:\t%v\n", edgesSize)
}

func EdgesStatReader(edgesStatfn string) (edgesSize int) {
	edgesStatfp, err := os.Open(edgesStatfn)
	if err != nil {
		log.Fatalf("[NodesStatReader] file %s Open error: %v\n", edgesStatfn, err)
	}
	defer edgesStatfp.Close()
	_, err = fmt.Fscanf(edgesStatfp, "edges size:\t%v\n", &edgesSize)
	if err != nil {
		log.Fatalf("[edgesStatReader] edges size parse error: %v\n", err)
	}
	return
}

func NodeMapMmapWriter(nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, nodesfn string) {
	nodesfp, err := os.Create(nodesfn)
	if err != nil {
		log.Fatalf("[NodeMapMmapWriter] file %s create error, err: %v\n", nodesfn, err)
	}
	defer nodesfp.Close()
	enc := gob.NewEncoder(nodesfp)
	err = enc.Encode(nodeMap)
	if err != nil {
		log.Fatalf("[NodeMapMmapWriter] encode err: %v\n", err)
	}
}

func NodeMapMmapReader(nodesfn string) (nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode) {
	nodesfp, err := os.Open(nodesfn)
	if err != nil {
		log.Fatalf("[NodeMapMmapReader] open file %s failed, err:%v\n", nodesfn, err)
	}
	defer nodesfp.Close()
	dec := gob.NewDecoder(nodesfp)
	err = dec.Decode(&nodeMap)
	if err != nil {
		log.Fatalf("[NodeMapMmapReader] decode failed, err: %v\n", err)
	}

	return
}

func NodesArrWriter(nodesArr []DBGNode, nodesfn string) {
	nodesfp, err := os.Create(nodesfn)
	if err != nil {
		log.Fatalf("[NodesArrWriter] file %s create error, err: %v\n", nodesfn, err)
	}
	defer nodesfp.Close()
	enc := gob.NewEncoder(nodesfp)
	err = enc.Encode(nodesArr)
	if err != nil {
		log.Fatalf("[NodeMapMmapWriter] encode err: %v\n", err)
	}
}

func NodesArrReader(nodesfn string) (nodesArr []DBGNode) {
	nodesfp, err := os.Open(nodesfn)
	if err != nil {
		log.Fatalf("[NodeMapMmapReader] open file %s failed, err:%v\n", nodesfn, err)
	}
	defer nodesfp.Close()
	dec := gob.NewDecoder(nodesfp)
	err = dec.Decode(&nodesArr)
	if err != nil {
		log.Fatalf("[NodeMapMmapReader] decode failed, err: %v\n", err)
	}

	return
}

func NodeMap2NodeArr(nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, nodesArr []DBGNode) {
	naLen := DBG_MAX_INT(len(nodesArr))
	for _, v := range nodeMap {
		if v.ID >= naLen {
			log.Fatalf("[NodeMap2NodeArr] v.ID: %v >= nodesArr len: %v\n", v.ID, naLen)
		}
		if v.ID <= 1 || v.ID == math.MaxUint32 {
			continue
		}
		nodesArr[v.ID] = v
	}
}
func writeComplexNodesToFile(complexNodesFn string, wc chan DBGNode, numCPU int) (complexNodeNum int) {
	ckfp, err := os.Create(complexNodesFn)
	if err != nil {
		log.Fatal(err)
	}
	defer ckfp.Close()
	//brfp := cbrotli.NewWriter(ckfp, cbrotli.WriterOptions{Quality: 1})
	//defer brfp.Close()
	buffp := bufio.NewWriterSize(ckfp, 1<<25)
	// if err != nil {
	// 	log.Fatal(err)
	// }
	endFlagCount := 0

	// write complex node to the file
	// complexNodeNum := 0
	for {
		nd := <-wc
		if len(nd.Seq) == 0 {
			endFlagCount++
			if endFlagCount == numCPU {
				break
			} else {
				continue
			}
		}

		//fmt.Printf("node: %v\n", nd)
		if err := binary.Write(buffp, binary.LittleEndian, nd.Seq); err != nil {
			log.Fatalf("[CDBG] write node seq to file err: %v\n", err)
		}
		if err := binary.Write(buffp, binary.LittleEndian, nd.EdgeIDIncoming); err != nil {
			log.Fatalf("[CDBG] write node seq to file err: %v\n", err)
		}
		if err := binary.Write(buffp, binary.LittleEndian, nd.EdgeIDOutcoming); err != nil {
			log.Fatalf("[CDBG] write node seq to file err: %v\n", err)
		}
		// *** test code ***
		/* extRB := constructcf.ExtendReadBnt2Byte(rb)
		fmt.Fprintf(os.Stderr, ">%v\n%v\n", complexNodeNum+1, Transform2Letters(extRB.Seq))
		*/
		// *** test code ***
		// ckgzfp.Write(rb.Seq)
		// ckgzfp.Write([]byte("\n"))
		complexNodeNum++
	}

	if err := buffp.Flush(); err != nil {
		log.Fatalf("[writeComplexNodesToFile] write to file: %s err: %v\n", complexNodesFn, err)
	}
	/*if err := brfp.Flush(); err != nil {
		log.Fatalf("[writeComplexNodesToFile] write to file: %s err: %v\n", complexNodesbrFn, err)
	}*/

	return
}

func CDBG(c cli.Command) {
	fmt.Println(c.Flags(), c.Parent().Flags())

	// get set arguments
	// t0 := time.Now()
	numCPU, err := strconv.Atoi(c.Parent().Flag("t").String())
	if err != nil {
		log.Fatalf("[CDBG] the argument 't' set error, err: %v\n", err)
	}

	klen, err := strconv.Atoi(c.Parent().Flag("K").String())
	if err != nil {
		log.Fatalf("[CDBG] the argument 'K' set error, err: %v\n", err)
	}
	if klen >= NODEMAP_KEY_LEN*32 {
		log.Fatalf("[CDBG] the argument 'K' must small than [NODEMAP_KEY_LEN * 32]: %v\n", NODEMAP_KEY_LEN*32)
	}
	runtime.GOMAXPROCS(numCPU)
	prefix := c.Parent().Flag("p").String()
	// create cpu profile
	/*profileFn := prefix + ".CDBG.prof"
	cpuprofilefp, err := os.Create(profileFn)
	if err != nil {
		log.Fatalf("[CDBG] open cpuprofile file: %v failed\n", profileFn)
	}
	pprof.StartCPUProfile(cpuprofilefp)
	defer pprof.StopCPUProfile()*/
	// cfinfofn := prefix + ".cfInfo"
	// cf, err := cuckoofilter.RecoverCuckooFilterInfo(cfinfofn)
	// if err != nil {
	// 	log.Fatalf("[CDGB] cuckoofilter recover err: %v\n", err)
	// }

	// find complex Nodes
	cfInfofn := prefix + ".cf.Info"
	cf, err := cuckoofilter.RecoverCuckooFilterInfo(cfInfofn)
	if err != nil {
		log.Fatalf("[CDBG] Read CuckooFilter info file: %v err: %v\n", cfInfofn, err)
	}
	cf.Hash = make([]cuckoofilter.Bucket, cf.NumItems)
	fmt.Printf("[CDBG]cf.NumItems: %v, cf.Kmerlen: %v, len(cf.Hash): %v\n", cf.NumItems, cf.Kmerlen, len(cf.Hash))
	cffn := prefix + ".cf.Hash.br"
	err = cf.HashReader(cffn)
	if err != nil {
		log.Fatalf("[CDBG] Read CuckooFilter Hash file: %v err: %v\n", cffn, err)
	}
	cf.GetStat()
	// fmt.Printf("[CDBG] cf.Hash[0]: %v\n", cf.Hash[0])
	//Kmerlen = cf.Kmerlen
	bufsize := 20
	cs := make(chan constructcf.KmerBntBucket, bufsize)
	wc := make(chan DBGNode, bufsize*50)
	uniqkmerbrfn := prefix + ".uniqkmerseq.br"
	// read uniq kmers form file
	go readUniqKmer(uniqkmerbrfn, cs, cf.Kmerlen, numCPU)

	// identify complex Nodes
	for i := 0; i < numCPU; i++ {
		go paraLookupComplexNode(cs, wc, cf)
	}

	// write complex Nodes to the file
	complexKmerfn := prefix + ".complexNode"
	complexNodeNum := writeComplexNodesToFile(complexKmerfn, wc, numCPU)
	fmt.Printf("[CDBG] found complex Node num is : %d\n", complexNodeNum)

	// construct Node map
	NBntUint64Len := (cf.Kmerlen - 1 + bnt.NumBaseInUint64 - 1) / bnt.NumBaseInUint64
	if NBntUint64Len > NODEMAP_KEY_LEN {
		log.Fatalf("[CDBG] nodeMap just allow max kmerlen is: %d, kmer set: %d\n", NODEMAP_KEY_LEN*32, cf.Kmerlen)
	}
	nodeMap := make(map[[NODEMAP_KEY_LEN]uint64]DBGNode)
	nodeID := constructNodeMap(complexKmerfn, nodeMap, NBntUint64Len)
	fmt.Printf("[CDBG] assgin nodeID to : %d\n", nodeID)
	// parallel generate edges and write to file
	edgefn := prefix + ".edges.fq"
	//numCPU = 1
	newNodeID, edgeID := GenerateDBGEdges(nodeMap, cf, edgefn, numCPU, nodeID)
	DBGStatfn := prefix + ".DBG.stat"
	DBGStatWriter(DBGStatfn, newNodeID, edgeID)
	// write DBG node map to the file
	nodesfn := prefix + ".nodes.mmap"
	NodeMapMmapWriter(nodeMap, nodesfn)
}

func ParseEdge(edgesbuffp *bufio.Reader) (edge DBGEdge, err error) {
	line1, err1 := edgesbuffp.ReadString('\n')
	line2, err2 := edgesbuffp.ReadString('\n')
	edgesbuffp.ReadString('\n')
	line3, err3 := edgesbuffp.ReadString('\n')
	if err1 != nil || err2 != nil || err3 != nil {
		if err1 == io.EOF {
			//err1 = nil
			err = io.EOF
			return
		} else {
			log.Fatalf("[ParseEdge] Read edge found err1,err2,err3: %v,%v,%v\n\tline1: %v\n\tline2: %v\n\tline3: %v\n", err1, err2, err3, line1, line2, line3)
		}
	}
	if _, err4 := fmt.Sscanf(string(line1), "@%d\t%d\t%d\n", &edge.ID, &edge.StartNID, &edge.EndNID); err4 != nil {
		log.Fatalf("[ParseEdge] fmt.Sscaf line1:%s err:%v\n", line1, err4)
	}
	// _, err4 = fmt.Sscanf(string(line2), "%s\n", &edge.Utg.Ks)
	// if err4 != nil {
	// 	log.Fatalf("[ParseEdge] Sscaf line2 err:%v\n", err4)
	// }
	// change char base to Bnt
	sline2 := string(line2[:len(line2)-1])
	edge.Utg.Ks = make([]byte, len(sline2))
	for i, v := range sline2 {
		edge.Utg.Ks[i] = bnt.Base2Bnt[v]
	}
	sline3 := string(line3[:len(line3)-1])
	edge.Utg.Kq = make([]uint8, len(sline3))
	for i, v := range sline3 {
		q := v - 33
		edge.Utg.Kq[i] = uint8(q)
	}
	if len(edge.Utg.Ks) != len(edge.Utg.Kq) {
		log.Fatalf("[ParseEdge] len(edge.Utg.Ks): %v != len(edge.Utg.Kq): %v\n", len(edge.Utg.Ks), len(edge.Utg.Ks))
	}

	return
}

func ReadEdgesFromFile(edgesfn string, edgesSize DBG_MAX_INT) (edgesArr []DBGEdge) {
	edgesArr = make([]DBGEdge, edgesSize)
	edgesfp, err := os.Open(edgesfn)
	if err != nil {
		log.Fatalf("[ReadEdgesFromFile] open file %s failed, err: %v\n", edgesfn, err)
	}
	defer edgesfp.Close()
	// edgesgzfp, err := gzip.NewReader(edgesfp)
	// if err != nil {
	// 	log.Fatalf("[ReadEdgesFromFile] read gz file: %s failed, err: %v\n", edgesfn, err)
	// }
	// defer edgesgzfp.Close()
	edgesbuffp := bufio.NewReader(edgesfp)

	var edgesNum int
	for {
		edge, err := ParseEdge(edgesbuffp)
		// num++
		//fmt.Printf("[ParseEdge] edge.ID: %v\n", edge.ID)
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatalf("[ParseEdge] file: %v, parse edge err: %v\n", edgesfn, err)
		}
		edgesArr[edge.ID] = edge
		edgesNum++
	}

	fmt.Printf("[ReadEdgesFromFile] found edge number is : %v\n", edgesNum)
	return
}

func GetRCUnitig(u Unitig) (ru Unitig) {
	ru.Ks = GetReverseCompByteArr(u.Ks)
	ru.Kq = make([]uint8, len(u.Kq))
	copy(ru.Kq, u.Kq)
	ReverseUint8Arr(ru.Kq)
	return
}

func RevNode(node DBGNode, kmerlen int) DBGNode {
	rnode := node
	var nBnt constructcf.KmerBnt
	nBnt.Seq = rnode.Seq
	nBnt.Len = kmerlen - 1
	rs := constructcf.ReverseComplet(nBnt)
	rnode.Seq = rs.Seq
	for i := 0; i < bnt.BaseTypeNum; i++ {
		rnode.EdgeIDIncoming[i] = node.EdgeIDOutcoming[bnt.BntRev[i]]
		rnode.EdgeIDOutcoming[i] = node.EdgeIDIncoming[bnt.BntRev[i]]
		// rnode.EdgeIDOutcoming[bnt.BntRev[i]] = node.EdgeIDOutcoming[bnt.BaseTypeNum-1-i], node.EdgeIDIncoming[i]
	}

	return rnode
}

/*func ReconstructConsistenceDBG(nodeMap map[string]DBGNode, edgesArr []DBGEdge) {
	for k, v := range nodeMap {
		stk := list.New()
		if v.GetProcessFlag() == 0 {
			v.SetProcessFlag()
			// fmt.Printf("[ReconstructConsistenceDBG] v: %v\n", v)
			nodeMap[k] = v
			stk.PushBack(v)
			for stk.Len() > 0 {
				// Pop a element from stack
				e := stk.Back()
				stk.Remove(e)
				node := e.Value.(DBGNode)
				// fmt.Printf("[ReconstructConsistenceDBG] Pop node: %v\n", node)
				// Processed flag
				if node.GetProcessFlag() != 1 {
					log.Fatalf("[ReconstructConsistenceDBG] node have not been set processed flag, node: %v\n", node)
				}
				for i := 0; i < bnt.BaseTypeNum; i++ {
					if node.EdgeIDOutcoming[i] > 0 {
						eid := node.EdgeIDOutcoming[i]
						if edgesArr[eid].GetProcessFlag() == 0 {
							if edgesArr[eid].StartNID != node.ID {
								// Debug code start
								// if edgesArr[eid].EndNID != node.ID {
								// 	log.Fatalf("[ReconstructConsistenceDBG] edgesArr[eid].EndNID != node.ID\n")
								// }
								// Debug code end
								// fmt.Printf("[ReconstructConsistenceDBG] before RCEdge edge: %v\n", edgesArr[eid])
								RCEdge(edgesArr, eid)
								// fmt.Printf("[ReconstructConsistenceDBG] after RCEdge edge: %v\n", edgesArr[eid])
							}
							if edgesArr[eid].EndNID > 0 {
								var tBnt constructcf.ReadBnt
								tBnt.Seq = edgesArr[eid].Utg.Ks[len(edgesArr[eid].Utg.Ks)-Kmerlen+1:]

								ks := constructcf.GetReadBntKmer(tBnt, 0, Kmerlen-1)
								rs := constructcf.ReverseComplet(ks)
								min := ks
								if min.BiggerThan(rs) {
									min = rs
								}
								if v2, ok := nodeMap[string(min.Seq)]; ok {
									var v2Bnt constructcf.ReadBnt
									v2Bnt.Seq = v2.Seq
									v2Bnt.Length = Kmerlen - 1
									if v2.GetProcessFlag() == 1 {
										if reflect.DeepEqual(v2Bnt, ks) == false {
											// log.Fatalf("[ReconstructConsistenceDBG] found not consistence node\n")
											fmt.Printf("[ReconstructConsistenceDBG] found not consistence node, edge: %v\nv1: %v\nv2: %v\n", edgesArr[eid], node, v2)
											if eid == 2870 {
												fmt.Printf("[ReconstructConsistenceDBG] edge: %v\n", edgesArr[8014])
											}
											fmt.Printf("[ReconstructConsistenceDBG] edge start: %v\nedge end: %v\n", edgesArr[eid].Utg.Ks[:Kmerlen], edgesArr[eid].Utg.Ks[len(edgesArr[eid].Utg.Ks)-Kmerlen:])
											var v1Bnt constructcf.ReadBnt
											v1Bnt.Seq = node.Seq
											v1Bnt.Length = Kmerlen - 1
											fmt.Printf("[ReconstructConsistenceDBG] v1.Seq: %v\n", constructcf.ExtendReadBnt2Byte(v1Bnt))
											fmt.Printf("[ReconstructConsistenceDBG] v2.Seq: %v\n", constructcf.ExtendReadBnt2Byte(v2Bnt))
										}
									} else {
										if reflect.DeepEqual(v2Bnt, ks) == false {
											v2 = RevNode(v2)
										}
										v2.SetProcessFlag()
										nodeMap[string(min.Seq)] = v2
										stk.PushBack(v2)

									}

								} else {
									log.Fatalf("[ReconstructConsistenceDBG] not found edge' end node, edge: %v\n", edgesArr[eid])
								}
							}
							edgesArr[eid].SetProcessFlag()
						}
					}

					if node.EdgeIDIncoming[i] > 0 {
						eid := node.EdgeIDIncoming[i]
						if edgesArr[eid].GetProcessFlag() == 0 {
							if edgesArr[eid].EndNID != node.ID {
								RCEdge(edgesArr, eid)
							}
							if edgesArr[eid].StartNID > 0 {
								var tBnt constructcf.ReadBnt
								tBnt.Seq = edgesArr[eid].Utg.Ks[:Kmerlen-1]
								ks := constructcf.GetReadBntKmer(tBnt, 0, Kmerlen-1)
								rs := constructcf.ReverseComplet(ks)
								min := ks
								if ks.BiggerThan(rs) {
									min = rs
								}

								if v2, ok := nodeMap[string(min.Seq)]; ok {
									var v2Bnt constructcf.ReadBnt
									v2Bnt.Seq = v2.Seq
									v2Bnt.Length = Kmerlen - 1
									if v2.GetProcessFlag() == 1 {
										if reflect.DeepEqual(v2Bnt, ks) == false {
											// log.Fatalf("[ReconstructConsistenceDBG] found not consistence node\n")
											fmt.Printf("[ReconstructConsistenceDBG] found not consistence node, edge: %v\nv1: %v\nv2: %v\n", edgesArr[eid], node, v2)
										}
									} else {
										if reflect.DeepEqual(v2Bnt, ks) == false {
											v2 = RevNode(v2)
										}
										v2.SetProcessFlag()
										nodeMap[string(min.Seq)] = v2
										stk.PushBack(v2)
									}
								} else {
									log.Fatalf("[ReconstructConsistenceDBG] not found edge' start node, edge: %v\n", edgesArr[eid])
								}
							}
							edgesArr[eid].SetProcessFlag()
						}
					}
				}
			}
		}
	}
}*/

/*func Coming2String(coming [bnt.BaseTypeNum]DBG_MAX_INT) (cs string) {
	for _, v := range coming {
		cs += " " + strconv.Itoa(int(v))
	}
	return
}*/

func GraphvizDBGArr(nodesArr []DBGNode, edgesArr []DBGEdge, graphfn string) {
	// create a new graph
	g := gographviz.NewGraph()
	g.SetName("G")
	g.SetDir(true)
	g.SetStrict(false)
	for _, v := range nodesArr {
		if v.ID < 2 || v.GetDeleteFlag() > 0 {
			continue
		}
		attr := make(map[string]string)
		attr["color"] = "Green"
		attr["shape"] = "record"
		var labels string
		//labels = "{<f0>" + strconv.Itoa(int(v.EdgeIDIncoming[0])) + "|<f1>" + strconv.Itoa(int(v.EdgeIDIncoming[1])) + "|<f2>" + strconv.Itoa(int(v.EdgeIDIncoming[2])) + "|<f3>" + strconv.Itoa(int(v.EdgeIDIncoming[3])) + "}|" + strconv.Itoa(int(v.ID)) + "|{<f0>" + strconv.Itoa(int(v.EdgeIDOutcoming[0])) + "|<f1>" + strconv.Itoa(int(v.EdgeIDOutcoming[1])) + "|<f2>" + strconv.Itoa(int(v.EdgeIDOutcoming[2])) + "|<f3>" + strconv.Itoa(int(v.EdgeIDOutcoming[3])) + "}"
		labels = "\"{" + strconv.Itoa(int(v.EdgeIDIncoming[0])) +
			"|" + strconv.Itoa(int(v.EdgeIDIncoming[1])) +
			"|" + strconv.Itoa(int(v.EdgeIDIncoming[2])) +
			"|" + strconv.Itoa(int(v.EdgeIDIncoming[3])) +
			"}|" + strconv.Itoa(int(v.ID)) +
			"| {" + strconv.Itoa(int(v.EdgeIDOutcoming[0])) +
			"|" + strconv.Itoa(int(v.EdgeIDOutcoming[1])) +
			"|" + strconv.Itoa(int(v.EdgeIDOutcoming[2])) +
			"|" + strconv.Itoa(int(v.EdgeIDOutcoming[3])) + "}\""
		attr["label"] = labels
		g.AddNode("G", strconv.Itoa(int(v.ID)), attr)
	}
	g.AddNode("G", "0", nil)
	//fmt.Printf("[GraphvizDBGArr] finished Add Nodes\n")

	for i := 1; i < len(edgesArr); i++ {
		e := edgesArr[i]
		if e.ID < 2 || e.GetDeleteFlag() > 0 {
			continue
		}
		attr := make(map[string]string)
		attr["color"] = "Blue"
		//labels := strconv.Itoa(int(e.ID)) + "len" + strconv.Itoa(len(e.Utg.Ks))
		//labels := strconv.Itoa(int(e.ID))
		labels := "\"ID:" + strconv.Itoa(int(e.ID)) + " len:" + strconv.Itoa(len(e.Utg.Ks)) + "\""
		attr["label"] = labels
		g.AddEdge(strconv.Itoa(int(e.StartNID)), strconv.Itoa(int(e.EndNID)), true, attr)
	}
	//fmt.Printf("[GraphvizDBGArr] finished Add edges\n")
	// output := graph.String()
	gfp, err := os.Create(graphfn)
	if err != nil {
		log.Fatalf("[GraphvizDBG] Create file: %s failed, err: %v\n", graphfn, err)
	}
	defer gfp.Close()
	gfp.WriteString(g.String())
}

func IsContainCycleEdge(nd DBGNode) bool {
	var arrIn, arrOut []DBG_MAX_INT
	for _, eID := range nd.EdgeIDIncoming {
		if eID > 1 {
			arrIn = append(arrIn, eID)
		}
	}
	for _, eID := range nd.EdgeIDOutcoming {
		if eID > 1 {
			arrOut = append(arrOut, eID)
		}
	}

	for _, eID1 := range arrIn {
		for _, eID2 := range arrOut {
			if eID1 == eID2 {
				return true
			}
		}
	}

	return false
}

func GetEdgeIDComing(coming [bnt.BaseTypeNum]DBG_MAX_INT) (num int, edgeID DBG_MAX_INT) {
	for _, v := range coming {
		if v > 1 {
			num++
			edgeID = v
		}
	}

	return
}

func ConcatEdges(u1, u2 Unitig, kmerlen int) (u Unitig) {
	u.Ks = make([]byte, len(u1.Ks)+len(u2.Ks)-(kmerlen-1))
	u.Kq = make([]uint8, len(u1.Kq)+len(u2.Kq)-(kmerlen-1))

	if !reflect.DeepEqual(u1.Ks[len(u1.Ks)-kmerlen+1:], u2.Ks[:kmerlen-1]) {
		log.Fatalf("[ConcatEdges] u1: %v, u2: %v can not concatenatable\n", u1.Ks[len(u1.Ks)-kmerlen+1:], u2.Ks[:kmerlen-1])
	}
	if len(u1.Ks) != len(u1.Kq) {
		log.Fatalf("[ConcatEdges] len(u1.Ks): %v != len(u1.Kq): %v\n", len(u1.Ks), len(u1.Kq))
	}
	if len(u2.Ks) != len(u2.Kq) {
		log.Fatalf("[ConcatEdges] len(u2.Ks): %v != len(u2.Kq): %v\n", len(u2.Ks), len(u2.Kq))
	}

	copy(u.Ks[:len(u1.Ks)], u1.Ks)
	copy(u.Ks[len(u1.Ks):], u2.Ks[kmerlen-1:])

	copy(u.Kq[:len(u1.Kq)], u1.Kq)
	for i := 0; i < kmerlen-1; i++ {
		u.Kq[len(u1.Kq)-(kmerlen-1)+i] += u2.Kq[i]
	}
	copy(u.Kq[len(u1.Kq):], u2.Kq[kmerlen-1:])

	return
}

/*func ConcatEdges(edgesArr []DBGEdge, inID, outID, dstID DBG_MAX_INT) {
	// check if is connective
	var inBnt, outBnt constructcf.ReadBnt
	fmt.Printf("[ConcatEdges] inID: %d, outID: %d, dstID: %d\nedgesArr[inID]:%v\nedgesArr[outID]:%v\n", inID, outID, dstID, edgesArr[inID], edgesArr[outID])
	inBnt.Seq = edgesArr[inID].Utg.Ks[len(edgesArr[inID].Utg.Ks)-Kmerlen+1:]
	inBnt.Length = len(inBnt.Seq)
	outBnt.Seq = edgesArr[outID].Utg.Ks[:Kmerlen-1]
	outBnt.Length = len(outBnt.Seq)
	//fmt.Printf("[ConcatEdges] inID: %d, outID: %d, dstID: %d\nseq1:%v\nseq2:%v\n", inID, outID, dstID, inBnt, outBnt)
	if reflect.DeepEqual(inBnt.Seq, outBnt.Seq) == false {
		log.Fatalf("[ConcatEdges] two edges is not connective\n\tin: %v\n\tout: %v\n", edgesArr[inID], edgesArr[outID])
	}
	if dstID == inID {
		u1, u2 := edgesArr[inID].Utg, edgesArr[outID].Utg
		edgesArr[inID].Utg.Ks = append(u1.Ks, u2.Ks[Kmerlen-1:]...)
		for i := 0; i < Kmerlen-1; i++ {
			if u1.Kq[len(u1.Kq)-Kmerlen+1+i] < u2.Kq[i] {
				u1.Kq[len(u1.Kq)-Kmerlen+1+i] = u2.Kq[i]
			}
		}
		edgesArr[inID].Utg.Kq = append(u1.Kq, u2.Kq[Kmerlen-1:]...)
		// DeleteEdgeID(nodesArr, edgesArr[inID].EndNID, inID)
		edgesArr[inID].EndNID = edgesArr[outID].EndNID

	} else {
		u1, u2 := edgesArr[inID].Utg, edgesArr[outID].Utg
		seq := make([]byte, len(u1.Ks))
		copy(seq, u1.Ks)
		edgesArr[outID].Utg.Ks = append(seq, u2.Ks[Kmerlen-1:]...)
		qul := make([]uint8, len(u1.Kq))
		copy(qul, u1.Kq)
		for i := 0; i < Kmerlen-1; i++ {
			if u1.Kq[len(u1.Kq)-Kmerlen+1+i] < u2.Kq[i] {
				qul[len(qul)-Kmerlen+1+i] = u2.Kq[i]
			}
		}
		edgesArr[outID].Utg.Kq = append(qul, u2.Kq[Kmerlen-1:]...)
		// DeleteEdgeID(nodesArr, edgesArr[outID].StartNID, outID)
		edgesArr[outID].StartNID = edgesArr[inID].StartNID
	}
}*/

/*func substituteEdgeID(nodeMap map[[NODEMAP_KEY_LEN]uint64]DBGNode, nodekey []uint64, srcID, dstID DBG_MAX_INT, kmerlen int) bool {
	var nkB constructcf.KmerBnt
	nkB.Seq = nodekey
	ks := constructcf.GetReadBntKmer(nkB, 0, kmerlen-1)
	rs := constructcf.ReverseComplet(ks)
	min := ks
	if ks.BiggerThan(rs) {
		min = rs
	}
	suc := false
	if nv, ok := nodeMap[string(min.Seq)]; ok {
		for i := 0; i < bnt.BaseTypeNum; i++ {
			if nv.EdgeIDIncoming[i] == srcID {
				nv.EdgeIDIncoming[i] = dstID
				suc = true
				break
			}
			if nv.EdgeIDOutcoming[i] == srcID {
				nv.EdgeIDOutcoming[i] = dstID
				suc = true
				break
			}
		}
		if suc == true {
			nodeMap[string(min.Seq)] = nv
		}
	} else {
		log.Fatalf("[substituteEdgeID] not found correct node\n")
	}

	return suc

}*/

/*func ChangeIDMapPathAndJoinPathArr(IDMapPath map[DBG_MAX_INT]uint32, joinPathArr []Path, e1, e2 DBGEdge, v DBGNode) []Path {
	var p, p1, p2 Path
	if idx, ok := IDMapPath[e1.ID]; ok {
		p1.IDArr = append(p1.IDArr, joinPathArr[idx].IDArr...)
		joinPathArr[idx].IDArr = nil
	} else {
		p1.IDArr = append(p1.IDArr, e1.ID)
	}

	if idx, ok := IDMapPath[e2.ID]; ok {
		p2.IDArr = append(p2.IDArr, joinPathArr[idx].IDArr...)
		joinPathArr[idx].IDArr = nil
	} else {
		p2.IDArr = append(p2.IDArr, e2.ID)
	}

	if e1.EndNID == v.ID {
		p.IDArr = append(p.IDArr, p1.IDArr...)
		tp := p2
		if e2.EndNID == v.ID {
			tp.IDArr = GetReverseDBG_MAX_INTArr(p2.IDArr)
		}
		p.IDArr = append(p.IDArr, tp.IDArr...)
	} else {
		tp := p2
		if e2.StartNID == v.ID {
			tp.IDArr = GetReverseDBG_MAX_INTArr(p2.IDArr)
		}
		p.IDArr = append(p.IDArr, tp.IDArr...)
		p.IDArr = append(p.IDArr, p1.IDArr...)
	}
	joinPathArr = append(joinPathArr, p)
	ni := uint32(len(joinPathArr)-1)
	IDMapPath[e1.ID] = ni
	IDMapPath[p.IDArr[0]]

	return joinPathArr
} */

func CleanDBGNodeEdgeIDComing(nodesArr []DBGNode, nID DBG_MAX_INT) {
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if nodesArr[nID].EdgeIDIncoming[i] == 1 {
			nodesArr[nID].EdgeIDIncoming[i] = 0
		}
		if nodesArr[nID].EdgeIDOutcoming[i] == 1 {
			nodesArr[nID].EdgeIDOutcoming[i] = 0
		}
	}
}

func CleanDBGEdgeIDComing(nodesArr []DBGNode) {
	for i, v := range nodesArr {
		if i < 2 || v.GetDeleteFlag() > 0 {
			continue
		}
		CleanDBGNodeEdgeIDComing(nodesArr, v.ID)
	}
}

func MakeSelfCycleEdgeOutcomingToIncoming(nodesArr []DBGNode, edgesArr []DBGEdge, opt Options) {
	for i, e := range edgesArr {
		if i < 2 || e.GetDeleteFlag() > 0 {
			continue
		}
		if e.StartNID == e.EndNID { // self cycle edge
			nd := nodesArr[e.StartNID]
			bn := constructcf.GetReadBntKmer(e.Utg.Ks, 0, opt.Kmer-1)
			if !reflect.DeepEqual(bn.Seq, nd.Seq) {
				e.Utg.Ks = GetReverseCompByteArr(e.Utg.Ks)
				ReverseByteArr(e.Utg.Kq)
			}
			if (nd.EdgeIDOutcoming[e.Utg.Ks[opt.Kmer-1]] != e.ID) || (nd.EdgeIDIncoming[e.Utg.Ks[len(e.Utg.Ks)-opt.Kmer]] != e.ID) {
				log.Fatalf("[MakeSelfCycleEdgeOutcomingToIncoming] error cycle edge set, e: %v\n\tv: %v\n", e, nd)
			}

		}
	}
}

func SmfyDBG(nodesArr []DBGNode, edgesArr []DBGEdge, opt Options) {
	kmerlen := opt.Kmer
	deleteNodeNum, deleteEdgeNum := 0, 0
	longTipsEdgesNum := 0

	// clean DBG EdgeIDComing
	CleanDBGEdgeIDComing(nodesArr)

	for i, v := range nodesArr {
		if i < 2 || v.GetDeleteFlag() > 0 {
			continue
		}
		if IsContainCycleEdge(v) {
			fmt.Printf("[SmfyDBG]node ID: %v is contain Cycle Edge\n", v.ID)
			continue
		}
		//fmt.Printf("[SmfyDBG] v: %v\n", v)
		inNum, inID := GetEdgeIDComing(v.EdgeIDIncoming)
		outNum, outID := GetEdgeIDComing(v.EdgeIDOutcoming)
		if inNum == 0 && outNum == 0 {
			nodesArr[i].SetDeleteFlag()
			deleteNodeNum++
		} else if inNum+outNum == 1 {
			id := inID
			if outNum == 1 {
				id = outID
			}
			//fmt.Printf("[SmfyDBG]v: %v,id: %v\n", v, id)
			//fmt.Printf("[SmfyDBG]edgesArr[%v]: %v\n",id, edgesArr[id])
			if edgesArr[id].StartNID == v.ID {
				edgesArr[id].StartNID = 0
			} else {
				edgesArr[id].EndNID = 0
			}
			if len(edgesArr[id].Utg.Ks) < opt.MaxNGSReadLen {
				edgesArr[id].SetDeleteFlag()
				deleteEdgeNum++
			} else {
				longTipsEdgesNum++
			}
			nodesArr[i].SetDeleteFlag()
			deleteNodeNum++
		} else if inNum == 1 && outNum == 1 && inID != outID { // prevent cycle ring
			e1 := edgesArr[inID]
			e2 := edgesArr[outID]
			//fmt.Printf("[SmfyDBG] e1: %v\n\te2: %v\n\tnd: %v\n", e1, e2, v)
			u1, u2 := e1.Utg, e2.Utg
			if e1.EndNID == v.ID {
				nID := e2.EndNID
				if e2.StartNID != v.ID {
					u2 = GetRCUnitig(u2)
					nID = e2.StartNID
				}
				//fmt.Printf("[SmfyDBG]v: %v\ne1.ID: %v, e1.StartNID: %v, e1.EndNID: %v, e2.ID:%v, e2.StartNID: %v, e2.EndNID: %v\n", v, e1.ID, e1.StartNID, e1.EndNID, e2.ID, e2.StartNID, e2.EndNID)
				edgesArr[inID].Utg = ConcatEdges(u1, u2, kmerlen)
				edgesArr[inID].EndNID = nID
				if nID > 0 && !SubstituteEdgeID(nodesArr, nID, e2.ID, e1.ID) {
					log.Fatalf("[SmfyDBG]v: %v\ne2.ID: %v substitute by e1.ID: %v failed, node: %v\n", v, e2.ID, e1.ID, nodesArr[nID])
				}
			} else {
				nID := e2.StartNID
				if e2.EndNID != v.ID {
					u2 = GetRCUnitig(u2)
					nID = e2.EndNID
				}
				//fmt.Printf("[SmfyDBG]v: %v\ne1.ID: %v, e1.StartNID: %v, e1.EndNID: %v, e2.ID:%v, e2.StartNID: %v, e2.EndNID: %v\n", v, e1.ID, e1.StartNID, e1.EndNID, e2.ID, e2.StartNID, e2.EndNID)
				edgesArr[inID].Utg = ConcatEdges(u2, u1, kmerlen)
				edgesArr[inID].StartNID = nID
				if nID > 0 && !SubstituteEdgeID(nodesArr, nID, e2.ID, e1.ID) {
					log.Fatalf("[SmfyDBG]v: %v\ne2.ID: %v substitute by e1.ID: %v failed, node: %v\n", v, e2.ID, e1.ID, nodesArr[nID])
				}
			}

			edgesArr[outID].SetDeleteFlag()
			deleteEdgeNum++
			nodesArr[v.ID].SetDeleteFlag()
			deleteNodeNum++
		}
	}

	// delete maybe short repeat edge than small than opt.MaxNGSReadLen
	for i, e := range edgesArr {
		if i < 2 || e.GetDeleteFlag() > 0 {
			continue
		}

		if e.StartNID == 0 && e.EndNID == 0 && len(e.Utg.Ks) < opt.MaxNGSReadLen {
			edgesArr[i].SetDeleteFlag()
			deleteEdgeNum++
		}
		// remove samll cycle maybe repeat
		/*if e.StartNID > 0 && e.StartNID == e.EndNID && len(e.Utg.Ks) < opt.MaxNGSReadLen {
			v := nodesArr[e.StartNID]
			inNum, inID := GetEdgeIDComing(v.EdgeIDIncoming)
			outNum, outID := GetEdgeIDComing(v.EdgeIDOutcoming)
			if inNum == 1 && outNum == 1 && inID == outID {
				edgesArr[i].SetDeleteFlag()
				deleteEdgeNum++
				nodesArr[v.ID].SetDeleteFlag()
				deleteNodeNum++
			}
		}*/
	}

	fmt.Printf("[SmfyDBG]deleted nodes number is : %d\n", deleteNodeNum)
	fmt.Printf("[SmfyDBG]deleted edges number is : %d\n", deleteEdgeNum)
	fmt.Printf("[SmfyDBG]long tips number is : %d\n", longTipsEdgesNum)
}

func CheckDBGSelfCycle(nodesArr []DBGNode, edgesArr []DBGEdge, kmerlen int) {
	for _, e := range edgesArr {
		if e.ID < 2 || e.StartNID < 2 || e.EndNID < 2 || e.GetDeleteFlag() > 0 {
			continue
		}
		if e.StartNID == e.EndNID {
			var nb constructcf.KmerBnt
			nb.Seq = nodesArr[e.StartNID].Seq
			nb.Len = kmerlen - 1
			extNb := constructcf.ExtendKmerBnt2Byte(nb)
			if reflect.DeepEqual(extNb, e.Utg.Ks[:kmerlen-1]) {

			} else {
				e.Utg.Ks = GetReverseCompByteArr(e.Utg.Ks)
				ReverseByteArr(e.Utg.Kq)
				fmt.Printf("[CheckDBGSelfCycle] ReverSeComp self cycle edge: %v\n", e.ID)
			}
		}
	}
}

func PrintTmpDBG(nodesArr []DBGNode, edgesArr []DBGEdge, prefix string) {
	nodesfn := prefix + ".tmp.nodes"
	edgesfn := prefix + ".tmp.edges"
	nodesfp, err := os.Create(nodesfn)
	if err != nil {
		log.Fatalf("[PrintTmpDBG] failed to create file: %s, err: %v\n", nodesfn, err)
	}
	defer nodesfp.Close()
	edgesfp, err := os.Create(edgesfn)
	if err != nil {
		log.Fatalf("[PrintTmpDBG] failed to create file: %s, err: %v\n", edgesfn, err)
	}
	defer edgesfp.Close()

	for _, v := range nodesArr {
		s := fmt.Sprintf("ID: %v, EdgeIncoming: %v, EdgeOutcoming: %v\n", v.ID, v.EdgeIDIncoming, v.EdgeIDOutcoming)
		nodesfp.WriteString(s)
	}

	for _, v := range edgesArr {
		s := fmt.Sprintf("ID: %v, StartNID: %v, EndNID: %v\n", v.ID, v.StartNID, v.EndNID)
		edgesfp.WriteString(s)
	}

}

func Transform2QSeq(utg Unitig) alphabet.QLetters {
	if len(utg.Ks) != len(utg.Kq) {
		log.Fatalf("[transform2QSeq] len(ks):%d != len(kq):%d\n", len(utg.Ks), len(utg.Kq))
	}
	qs := make(alphabet.QLetters, len(utg.Ks))
	for i := 0; i < len(utg.Ks); i++ {
		var ql alphabet.QLetter
		ql.L = alphabet.Letter(bnt.BitNtCharUp[utg.Ks[i]])
		ql.Q = alphabet.Qphred(utg.Kq[i])
		qs[i] = ql
	}

	return qs
}

func Transform2Letters(ks []byte) alphabet.Letters {
	ls := make(alphabet.Letters, len(ks))
	for i, b := range ks {
		//ls = append(ls, alphabet.Letter(bnt.BitNtCharUp[b]))
		ls[i] = alphabet.Letter(bnt.BitNtCharUp[b])
	}

	return ls
}

func Transform2Char(ks []byte) []byte {
	cs := make([]byte, len(ks))
	for i, b := range ks {
		//fmt.Printf("[Transform2Char] ks[%v]: %c\n", i, b)
		//ls = append(ls, alphabet.Letter(bnt.BitNtCharUp[b]))
		cs[i] = bnt.BitNtCharUp[b]
	}
	return cs
}

func Transform2BntByte(ks []byte) []byte {
	bs := make([]byte, len(ks))
	for i, c := range ks {
		bs[i] = bnt.Base2Bnt[c]
	}
	return bs
}

func Transform2Unitig(Seq alphabet.QLetters, qual bool) (utg Unitig) {

	utg.Ks = make([]byte, len(Seq))
	if qual {
		utg.Kq = make([]uint8, len(Seq))
	}
	for i, v := range Seq {
		utg.Ks[i] = bnt.Base2Bnt[v.L]
		if qual {
			utg.Kq[i] = uint8(v.Q)
		}
	}

	return utg
}

var AdpaterSeq = "cggccgcaaggggttcgcgtcagcgggtgttggcgggtgtcggggctggcttaactatgcggcatcagagcagattgtactgagagtgcaccatatgcggtgtgaaataccacacagatgcgtaaggagaaaataccgcatcaggcgccattcgccattcagctgcgcaactgttgggaagggcgatcggtgcgggcctc"

// Set default quality(default = 1)
func SetDefaultQual(seq Unitig) (new Unitig) {
	for i := 0; i < len(seq.Ks); i++ {
		seq.Ks[i] = bnt.Base2Bnt[seq.Ks[i]]
		seq.Kq = append(seq.Kq, uint8(1))
	}

	return seq
}
func StoreEdgesToFn(edgesfn string, edgesArr []DBGEdge) {
	fp, err := os.Create(edgesfn)
	if err != nil {
		log.Fatalf("[StoreEdgesToFn] create file: %s failed, err: %v\n", edgesfn, err)
	}
	defer fp.Close()

	fqfp := fastq.NewWriter(fp)
	for _, v := range edgesArr {
		if v.ID > 0 && v.GetDeleteFlag() == 0 {
			seq := linear.NewQSeq("", nil, alphabet.DNA, alphabet.Sanger)
			seq.ID = strconv.Itoa(int(v.ID))
			// Add start and end adapter seq
			qs := Transform2QSeq(v.Utg)
			seq.AppendQLetters(qs...)
			var path string
			/*if len(v.PathMat) > 0 && len(v.PathMat[0].IDArr) > 1 {
				for _, id := range v.PathMat[0].IDArr {
					path += strconv.Itoa(int(id)) + "-"
				}
				path = path[:len(path)-1]
			}*/
			ans := strconv.Itoa(int(v.StartNID)) + "\t" + strconv.Itoa(int(v.EndNID)) + "\tpath:" + path + "\tlen:" + strconv.Itoa(seq.Len())
			seq.Annotation.SetDescription(ans)
			_, err := fqfp.Write(seq)
			if err != nil {
				log.Fatalf("[StoreEdgesToFn] write seq: %v; err: %v\n", seq, err)
			}
		}
	}
}

func StoreMappingEdgesToFn(edgesfn string, edgesArr []DBGEdge, MaxMapEdgeLen int) {
	fp, err := os.Create(edgesfn)
	if err != nil {
		log.Fatalf("[StoreMappingEdgesToFn] create file: %s failed, err: %v\n", edgesfn, err)
	}
	defer fp.Close()

	fafp := fasta.NewWriter(fp, 80)
	for _, v := range edgesArr {
		if v.ID > 0 && v.GetDeleteFlag() == 0 {
			if len(v.Utg.Ks) > MaxMapEdgeLen {
				seq1 := linear.NewSeq("", nil, alphabet.DNA)
				seq2 := linear.NewSeq("", nil, alphabet.DNA)
				seq1.ID = strconv.Itoa(int(v.ID)) + "/1"
				seq2.ID = strconv.Itoa(int(v.ID)) + "/2"
				//fmt.Printf("[StoreMappingEdgesToFn] v.Utg.Ks[:MaxMapEdgeLen/2]: %v\n", v.Utg.Ks[:MaxMapEdgeLen/2])
				seq1.AppendLetters(Transform2Letters(v.Utg.Ks[:MaxMapEdgeLen/2])...)
				seq2.AppendLetters(Transform2Letters(v.Utg.Ks[len(v.Utg.Ks)-MaxMapEdgeLen/2:])...)
				ans := strconv.Itoa(int(v.StartNID)) + "\t" + strconv.Itoa(int(v.EndNID)) + "\tlen:" + strconv.Itoa(seq1.Len())
				seq1.Annotation.SetDescription(ans)
				seq2.Annotation.SetDescription(ans)
				_, err1 := fafp.Write(seq1)
				_, err2 := fafp.Write(seq2)
				if err1 != nil || err2 != nil {
					log.Fatalf("[StoreMappingEdgesToFn] write seq1: %v\n\tseq2: %v\n\terr1: %v\terr2: %v\n", seq1, seq2, err1, err2)
				}
			} else {
				//fmt.Printf("[StoreMappingEdgesToFn] v.Utg.Ks: %v\n", v.Utg.Ks)
				seq := linear.NewSeq("", nil, alphabet.DNA)
				seq.ID = strconv.Itoa(int(v.ID))
				la := Transform2Letters(v.Utg.Ks)
				seq.AppendLetters(la...)
				ans := strconv.Itoa(int(v.StartNID)) + "\t" + strconv.Itoa(int(v.EndNID)) + "\tlen:" + strconv.Itoa(seq.Len())
				seq.Annotation.SetDescription(ans)
				_, err := fafp.Write(seq)
				if err != nil {
					log.Fatalf("[StoreMappingEdgesToFn] write seq: %v; err: %v\n", seq, err)
				}
			}
		}
	}
}

func LoadEdgesfqFromFn(fn string, edgesArr []DBGEdge, qual bool) {
	fp, err := os.Open(fn)
	if err != nil {
		log.Fatalf("[LoadEdgesfaFromFn] open file: %s error: %v\n", fn, err)
	}
	defer fp.Close()
	fqfp := fastq.NewReader(fp, linear.NewQSeq("", nil, alphabet.DNA, alphabet.Sanger))
	for {
		if s, err := fqfp.Read(); err != nil {
			if err == io.EOF {
				break
			} else {
				log.Fatalf("[LoadEdgesfqFromFn] read file: %s error: %v\n", fn, err)
			}
		} else {
			l := s.(*linear.QSeq)
			var edge DBGEdge
			id, err := strconv.Atoi(l.ID)
			if err != nil {
				log.Fatalf("[LoadEdgesfqFromFn] parse Name:%s of fastq err: %v\n", l.ID, err)
			}
			edge.ID = DBG_MAX_INT(id)
			var ps string
			var lenKs int
			_, err = fmt.Sscanf(l.Description(), "%v\t%v\t%v\tlen:%d\n", &edge.StartNID, &edge.EndNID, &ps, &lenKs)
			if err != nil {
				log.Fatalf("[LoadEdgesfqFromFn] parse Description:%s of fastq err: %v\n", l.Description(), err)
			}
			if len(ps) > 5 {
				var path Path
				for _, item := range strings.Split(ps[5:], "-") { // ps[:5] == "path:"
					id, err := strconv.Atoi(item)
					if err != nil {
						log.Fatalf("[LoadEdgesFqFromFn] path: %v convert to int err: %v\n", ps, err)
					}
					path.IDArr = append(path.IDArr, DBG_MAX_INT(id))
				}
				edge.PathMat = append(edge.PathMat, path)
			}
			edge.Utg = Transform2Unitig(l.Seq, qual)
			if edge.ID >= DBG_MAX_INT(len(edgesArr)) {
				log.Fatalf("[LoadEdgesfqFromFn] edge.ID:%v >= len(edgesArr):%d\n", edge.ID, len(edgesArr))
			} else if edgesArr[edge.ID].ID > 0 {
				log.Fatalf("[LoadEdgesfqFromFn] the position: %v in edgesArr has value:%v\n", edge.ID, edgesArr[edge.ID])
			}
			edgesArr[edge.ID] = edge
		}

	}
}

func Set(ea1, ea2 []DBG_MAX_INT) []DBG_MAX_INT {
	arr := make([]DBG_MAX_INT, len(ea1))
	copy(arr, ea1)
	for _, id := range ea2 {
		j := 0
		for ; j < len(arr); j++ {
			if arr[j] == id {
				break
			}
		}
		if j == len(arr) {
			arr = append(arr, id)
		}
	}
	return arr
}

// set the unique edge of edgesArr
func SetDBGEdgesUniqueFlag(edgesArr []DBGEdge, nodesArr []DBGNode) (uniqueNum, semiUniqueNum, twoCycleNum, selfCycle int) {
	for i, e := range edgesArr {
		if e.ID < 2 || e.GetDeleteFlag() > 0 {
			continue
		}

		if e.StartNID == e.EndNID {
			selfCycle++
			continue
		}

		var ea1, ea2 []DBG_MAX_INT
		if e.StartNID > 0 {
			nd := nodesArr[e.StartNID]
			if IsInComing(nd.EdgeIDIncoming, e.ID) {
				ea1 = GetNearEdgeIDArr(nd, e.ID, true)
			} else {
				ea1 = GetNearEdgeIDArr(nd, e.ID, false)
			}
		}
		if e.EndNID > 0 {
			nd := nodesArr[e.EndNID]
			if IsInComing(nd.EdgeIDIncoming, e.ID) {
				ea2 = GetNearEdgeIDArr(nd, e.ID, true)
			} else {
				ea2 = GetNearEdgeIDArr(nd, e.ID, false)
			}
		}

		if (len(ea1) == 1 && len(ea2) == 1 && ea1[0] == ea2[0]) || (len(ea1) > 1 && len(ea2) > 1 && len(Set(ea1, ea2)) < len(ea1)+len(ea2)) {
			edgesArr[i].SetTwoEdgesCycleFlag()
			twoCycleNum++
			continue
		}

		if len(ea1) <= 1 && len(ea2) <= 1 {
			edgesArr[i].SetUniqueFlag()
			uniqueNum++
		} else if (len(ea1) == 1 && len(ea2) > 1) || (len(ea1) > 1 && len(ea2) == 1) {
			edgesArr[i].SetSemiUniqueFlag()
			semiUniqueNum++
		}
	}
	return
}

/*type EdgeMapInfo struct {
	ID DBG_MAX_INT
	Strand bool
}*/
type PathSeq struct {
	ID     DBG_MAX_INT
	NID    DBG_MAX_INT // the path end node ID
	Strand bool
	//Start, End int
}

type ReadMapInfo struct {
	ID           int64
	StartP, EndP int
	//NID          constructdbg.DBG_MAX_INT
	//Seq        []byte
	PathSeqArr []PathSeq
	//Strands    []bool
}

type AlignInfo struct {
	ID      int64
	EndPos  int
	Paths   []DBG_MAX_INT
	Strands []bool // strand for Paths
	Seq     []byte
}

func paraLoadNGSReads(brfn string, cs chan constructcf.ReadInfo, kmerLen int, we chan int) {
	var count int
	format := constructcf.GetReadsFileFormat(brfn)
	fp, err := os.Open(brfn)
	if err != nil {
		log.Fatalf("[paraLoadNGSReads] %v\n", err)
	}
	defer fp.Close()
	brfp := cbrotli.NewReaderSize(fp, 1<<25)
	defer brfp.Close()
	buffp := bufio.NewReader(brfp)
	var err1 error
	for err1 != io.EOF {
		ri, err1 := constructcf.GetReadFileRecord(buffp, format, false)
		if ri.ID == 0 {
			if err1 == io.EOF {
				break
			} else {
				log.Fatalf("[GetReadSeqBucket] file: %s encounter err: %v\n", brfn, err1)
			}
		}
		cs <- ri
		count++
	}
	we <- count
}

func LoadNGSReads(cfgFn string, correct bool, cs chan constructcf.ReadInfo, numCPU, kmerLen int) {
	cfgInfo, err := constructcf.ParseCfg(cfgFn, correct)
	if err != nil {
		log.Fatal("[constructcf.ParseCfg] found err")
	}
	fmt.Println(cfgInfo)

	var totalNumReads int

	// iterate cfgInfo find fastq files
	we := make(chan int, numCPU)
	for i := 0; i < numCPU; i++ {
		we <- 0
	}
	//var numT int
	for _, lib := range cfgInfo.Libs {
		// seqProfile == 1 note Illumina
		if lib.AsmFlag != constructcf.AllState && lib.SeqProfile != 1 {
			continue
		}
		for _, fn := range lib.FnName {
			totalNumReads += <-we
			go paraLoadNGSReads(fn, cs, kmerLen, we)
		}
	}

	// check child routinue have finishedT
	for i := 0; i < numCPU; i++ {
		totalNumReads += <-we
	}
	fmt.Printf("[LoadNGSReads] total processed number reads : %v\n", totalNumReads)

	// send map goroutinues finish signal
	close(cs)
}

func writeAlignToFile(wrFn string, wc chan AlignInfo, numCPU int) {
	fp, err := os.Create(wrFn)
	if err != nil {
		log.Fatalf("[writeAlignToFile] failed to create file: %s, err:%v\n", wrFn, err)
	}
	defer fp.Close()
	buffp := bufio.NewWriter(fp)

	var finishedT int
	for {
		ai := <-wc
		if len(ai.Paths) == 0 {
			finishedT++
			if finishedT == numCPU {
				break
			} else {
				continue
			}
		}
		ps := make([]string, len(ai.Paths))
		for i := 0; i < len(ai.Paths); i++ {
			ps[i] = strconv.Itoa(int(ai.Paths[i]))
		}
		s := fmt.Sprintf("%d\t%s\n", ai.ID, strings.Join(ps, ":"))
		buffp.WriteString(s)
	}

	if err = buffp.Flush(); err != nil {
		log.Fatalf("[writeAlignToFile] failed to Flush buffer file: %s, err:%v\n", wrFn, err)
	}
}

func BiggerThan(kb, rb []byte) bool {
	for i, b := range kb {
		if b > rb[i] {
			return true
		} else if b < rb[i] {
			return false
		}
	}
	return false
}

func GetCuckoofilterDBGSampleSize(edgesArr []DBGEdge, winSize, maxNGSReadLen, kmerLen int64) (cfSize int64) {

	for _, e := range edgesArr {
		if e.GetDeleteFlag() > 0 || e.ID < 2 {
			continue
		}
		//fmt.Printf("[GetCuckoofilterDBGSampleSize] e: %v\n", e)
		el := int64(len(e.Utg.Ks))
		cfSize += ((el - kmerLen + winSize - 1) / winSize) + 1
		/*if el < 2*maxNGSReadLen-kmerLen { // get whole length sliding windowns
			cfSize += ((el - kmerLen + winSize - 1) / winSize) + 1
		} else { // just sample two ends
			cfSize += ((maxNGSReadLen-kmerLen+winSize-1)/winSize + 1) * 2
		}*/
	}
	return cfSize
}

func GetMinimizer(seq []byte, kmerlen int) (minSeq []byte, pos int, strand bool) {
	for i := 0; i < len(seq)-kmerlen+1; i++ {
		kb := seq[i : i+kmerlen]
		rb := GetReverseCompByteArr(kb)
		st := PLUS
		if BiggerThan(kb, rb) {
			kb, rb = rb, kb
			st = MINUS
		}
		if len(minSeq) == 0 || BiggerThan(minSeq, kb) {
			minSeq = kb
			pos = i
			strand = st
		}
	}

	return
}

func ConstructCFDBGMinimizers(cf CuckooFilter, edgesArr []DBGEdge, winSize int, maxNGSReadLen int64) (count int) {
	for _, e := range edgesArr {
		if e.ID < 2 || e.GetDeleteFlag() > 0 {
			continue
		}
		el := len(e.Utg.Ks)
		maxLen := el
		if int64(el) > 2*maxNGSReadLen-int64(cf.Kmerlen) {
			maxLen = int(maxNGSReadLen)
		}
		//fmt.Printf("[constructCFDBGSample] e: %v\n", e)
		for j := 0; j < maxLen-cf.Kmerlen+1; j += winSize {
			z := j + cf.Kmerlen + winSize - 1
			if z > maxLen { // make the boundary kmer in the Samples
				z = maxLen
			}
			kb, pos, strand := GetMinimizer(e.Utg.Ks[j:z], cf.Kmerlen)

			//fmt.Printf("e.ID: %v\tmaxLen: %v\tj: %v,pos: %v, strand: %v, kb: %v\n", e.ID, maxLen, j, pos, strand, kb)
			suc := cf.Insert(kb, e.ID, uint32(j+pos), strand)
			if suc == false {
				log.Fatalf("[constructCFDBGSample] Insert to the CuckooFilter of DBGSample false\n")
			}
			count++
		}

		if int64(el) > 2*maxNGSReadLen-int64(cf.Kmerlen) {
			for j := el - int(maxNGSReadLen); j < el-cf.Kmerlen+1; j += winSize {
				z := j + cf.Kmerlen + winSize - 1
				if z > el { // make the boundary kmer in the Samples
					z = el
				}
				kb, pos, strand := GetMinimizer(e.Utg.Ks[j:z], cf.Kmerlen)

				suc := cf.Insert(kb, e.ID, uint32(j+pos), strand)
				if suc == false {
					log.Fatalf("[constructCFDBGSample] Insert to the CuckooFilter of DBGSample false\n")
				}
				count++
			}
		}
		//fmt.Printf("[constructCFDBGSample] count : %v\n", count)
	}

	return count
}

// found kmer seed position in the DBG edges
func LocateSeedKmerCF(cf CuckooFilter, ri constructcf.ReadInfo, winSize int, edgesArr []DBGEdge) (dbgK DBGKmer, pos int, strand bool) {
	MaxStepNum := 10
	if len(ri.Seq) < cf.Kmerlen+MaxStepNum*winSize {
		fmt.Printf("[LocateSeedKmerCF] read: %v sequence length smaller than KmerLen(%v) + 2 * winSize(%v)\n", ri, cf.Kmerlen, winSize)
		MaxStepNum = (len(ri.Seq) - cf.Kmerlen) / winSize
	}
	var kb []byte
	for i := 0; i < MaxStepNum; i++ {
		kb, pos, strand = GetMinimizer(ri.Seq[i*winSize:i*winSize+cf.Kmerlen+winSize-1], cf.Kmerlen)
		var count int
		dbgK, count = cf.Lookup(kb, edgesArr)
		pos += i * winSize
		if count == 1 && dbgK.GetCount() > 0 {
			return
		}
		if count > 1 {
			fmt.Printf("[LocateSeedKmerCF] found seed count: %v\n", count)
			dbgK.setCFItem(0, 0)
			break
		}
	}

	// search seed by read end partition
	/*if count > 1 {
		count = 0
		for i := 0; i < 2; i++ {
			startP := len(ri.Seq) - cf.Kmerlen - winSize + 1 - i*winSize
			kb, pos, strand = GetMinimizer(ri.Seq[startP:len(ri.Seq)-i*winSize], cf.Kmerlen)
			dbgK, count = cf.Lookup(kb, edgesArr)
			pos += uint32(startP)
			if count == 1 && dbgK.GetCount() > 0 {
				fmt.Printf("[LocateSeedKmerCF] found seed count: %v\n", count)
				return
			}
			if count > 1 {
				dbgK.setCFItem(0, 0)
				break
			}
		}
	} */

	/*// found the second window
	kb, pos, strand = GetMinimizer(ri.Seq[winSize:winSize+cf.Kmerlen+winSize-1], cf.Kmerlen)
	pos += uint32(winSize)
	dbgK = cf.Lookup(kb, edgesArr)*/

	return
}

const (
	IN  = true
	OUT = false
)

func IsInDBGNode(nd DBGNode, eID DBG_MAX_INT) bool {
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if nd.EdgeIDIncoming[i] == eID || nd.EdgeIDOutcoming[i] == eID {
			return true
		}
	}

	return false
}

func GetNextMappingEID(nd DBGNode, e DBGEdge, base byte) DBG_MAX_INT {
	if e.StartNID == nd.ID {
		if IsInComing(nd.EdgeIDOutcoming, e.ID) {
			return nd.EdgeIDIncoming[base]
		} else {
			return nd.EdgeIDOutcoming[bnt.BntRev[base]]
		}
	} else {
		if IsInComing(nd.EdgeIDIncoming, e.ID) {
			return nd.EdgeIDOutcoming[base]
		} else {
			return nd.EdgeIDIncoming[bnt.BntRev[base]]
		}
	}
}

/*func GetNextMappingEdgeInfo(e DBGEdge, strand bool, direction uint8, ne DBGEdge, node DBGNode, kmerlen int) (bool, int) {
	var edgeNodeSeq [kmerlen-1]byte
	if direction == BACKWARD {
		if strand == PLUS {
			copy(edgeNodeSeq, e.Utg.Ks[:kmerlen-1])
		} else {

		}
	} else { // FORWARD

	}
}*/

func GetComingOtherEArr(coming [bnt.BaseTypeNum]DBG_MAX_INT, eID DBG_MAX_INT) (ea []DBG_MAX_INT) {
	for _, id := range coming {
		if id > 1 && id != eID {
			ea = append(ea, id)
		}
	}
	return
}

func GetSelfCycleNextMapEdgeInfo(eID DBG_MAX_INT, nd DBGNode, edgesArr []DBGEdge, nodeSeq []byte, kmerlen int, direction uint8, base byte, correct bool) (ID DBG_MAX_INT, pos int, strand bool) {
	var tmp constructcf.KmerBnt
	//kb.Seq, kb.Len = nodeSeq, len(nodeSeq)
	nodeBnt := constructcf.GetReadBntKmer(nodeSeq, 0, kmerlen-1)
	revNdSeq := constructcf.ReverseComplet(nodeBnt)
	tmp.Seq, tmp.Len = nd.Seq, kmerlen-1
	ndBntSeq := constructcf.ExtendKmerBnt2Byte(tmp)
	if reflect.DeepEqual(nodeBnt.Seq, nd.Seq) {
		if direction == BACKWARD {
			if nd.EdgeIDIncoming[base] > 1 {
				ID = nd.EdgeIDIncoming[base]
			} else {
				if correct {
					ea := GetComingOtherEArr(nd.EdgeIDIncoming, eID)
					if len(ea) == 1 {
						ID = ea[0]
					} else {
						return
					}
				} else {
					return
				}
			}
			ne := edgesArr[ID]
			if ne.EndNID == nd.ID && reflect.DeepEqual(nodeSeq, ne.Utg.Ks[len(ne.Utg.Ks)-(kmerlen-1):]) {
				pos = len(ne.Utg.Ks) - (kmerlen - 1)
				strand = PLUS
				return
			}
			if ne.StartNID == nd.ID && reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[:kmerlen-1])) {
				pos = kmerlen - 1
				strand = MINUS
			} else {
				log.Fatalf("[GetSelfCycleNextMapEdgeInfo] ne of node seq != nodeSeq, ne: %v\n\tnodeSeq: %v", ne, nodeSeq)
			}
		} else { // direction == FORWARD
			if nd.EdgeIDOutcoming[base] > 1 {
				ID = nd.EdgeIDOutcoming[base]
			} else {
				if correct {
					ea := GetComingOtherEArr(nd.EdgeIDOutcoming, eID)
					if len(ea) == 1 {
						ID = ea[0]
					} else {
						return
					}
				} else {
					return
				}
			}
			ne := edgesArr[ID]
			if ne.StartNID == nd.ID && reflect.DeepEqual(nodeSeq, ne.Utg.Ks[:kmerlen-1]) {
				pos = kmerlen - 1
				strand = PLUS
				return
			}
			if ne.EndNID == nd.ID && reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[len(ne.Utg.Ks)-(kmerlen-1):])) {
				pos = len(ne.Utg.Ks) - (kmerlen - 1)
				strand = MINUS
			} else {
				log.Fatalf("[GetSelfCycleNextMapEdgeInfo] ne of node seq != nodeSeq, ne: %v\n\tnodeSeq: %v", ne, nodeSeq)
			}
		}
	} else if reflect.DeepEqual(revNdSeq.Seq, nd.Seq) {
		base = bnt.BntRev[base]
		if direction == BACKWARD {
			if nd.EdgeIDOutcoming[base] > 1 {
				ID = nd.EdgeIDOutcoming[base]
			} else {
				if correct {
					ea := GetComingOtherEArr(nd.EdgeIDOutcoming, eID)
					if len(ea) == 1 {
						ID = ea[0]
					} else {
						return
					}
				} else {
					return
				}
			}
			ne := edgesArr[ID]
			if ne.StartNID == nd.ID && reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[:kmerlen-1])) {
				pos = kmerlen - 1
				strand = MINUS
				return
			}
			if ne.EndNID == nd.ID && reflect.DeepEqual(nodeSeq, ne.Utg.Ks[len(ne.Utg.Ks)-(kmerlen-1):]) {
				pos = len(ne.Utg.Ks) - (kmerlen - 1)
				strand = PLUS
			} else {
				log.Fatalf("[GetSelfCycleNextMapEdgeInfo] ne of node revNdSeq != nodeSeq, ne: %v\n\tnodeSeq: %v", ne, revNdSeq)
			}
		} else { // direction == FORWARD
			if nd.EdgeIDIncoming[base] > 1 {
				ID = nd.EdgeIDIncoming[base]
			} else {
				if correct {
					ea := GetComingOtherEArr(nd.EdgeIDIncoming, eID)
					if len(ea) == 1 {
						ID = ea[0]
					} else {
						return
					}
				} else {
					return
				}
			}
			ne := edgesArr[ID]
			if ne.EndNID == nd.ID && reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[len(ne.Utg.Ks)-(kmerlen-1):])) {
				pos = len(ne.Utg.Ks) - (kmerlen - 1)
				strand = MINUS
				return
			}
			if ne.StartNID == nd.ID && reflect.DeepEqual(nodeSeq, ne.Utg.Ks[:kmerlen-1]) {
				pos = kmerlen - 1
				strand = PLUS
			} else {
				log.Fatalf("[GetSelfCycleNextMapEdgeInfo] ne of node revNdSeq != nodeSeq, ne: %v\n\tnodeSeq: %v", ne, revNdSeq)
			}
		}
	} else {
		log.Fatalf("[GetSelfCycleNextMapEdgeInfo] nd.Seq != nodeSeq, nd.Seq: %v\n\tnodeSeq: %v", ndBntSeq, nodeSeq)
	}

	return
}

/*func MappingReadToEdgesBackWard(dk DBGKmer, ri constructcf.ReadInfo, rpos int, rstrand bool, edgesArr []DBGEdge, nodesArr []DBGNode, kmerlen int, correct bool) (errorNum int, ai AlignInfo) {
	var strand bool
	if dk.Strand != rstrand {
		dk.Pos += kmerlen
		strand = MINUS
	} else {
		strand = PLUS
	}

	ai.Seq = make([]byte, rpos+kmerlen)
	copy(ai.Seq[rpos:rpos+kmerlen], ri.Seq[rpos:rpos+kmerlen])
	ai.EndPos = math.MinInt32
	if rpos == 0 {
		var ps PathSeq
		ps.Start = int(dk.Pos) + kmerlen - 1
		ps.End = int(dk.Pos) - 1
		ai.Paths = append(ai.Paths, dk.ID)
		ai.Strands = append(ai.Strands, strand)
		if strand == PLUS {
			ai.EndPos = int(dk.Pos) - 1
		} else {
			ai.EndPos = int(dk.Pos)
		}
		return
	}
	for i := rpos - 1; i >= 0; {
		e := edgesArr[dk.ID]
		ai.Paths = append(ai.Paths, e.ID)
		ai.Strands = append(ai.Strands, strand)
		b := 0
		var j int
		if strand == PLUS {
			if int(dk.Pos) < rpos {
				b = rpos - int(dk.Pos)
			}
			j = int(dk.Pos) - 1
			for ; i >= b; i-- {
				//fmt.Printf("[MappingReadToEdgesBackWard] i: %v, j: %v, dk.Pos: %v, pos: %v, b: %v\n", i, j, dk.Pos, rpos, b)
				if ri.Seq[i] != e.Utg.Ks[j] {
					errorNum++
					if !correct {
						break
					}
				}
				ai.Seq[i] = e.Utg.Ks[j]
				j--
			}
		} else { // strand == MINUS
			if len(e.Utg.Ks)-int(dk.Pos) < rpos {
				b = rpos - (len(e.Utg.Ks) - int(dk.Pos))
			}
			j = int(dk.Pos)
			for ; i >= b; i-- {
				//fmt.Printf("[MappingReadToEdgesBackWard] i: %v, j: %v, dk.Pos: %v, pos: %v, b: %v\n", i, j, dk.Pos, rpos, b)
				if ri.Seq[i] != bnt.BntRev[e.Utg.Ks[j]] {
					errorNum++
					if !correct {
						break
					}
				}
				ai.Seq[i] = bnt.BntRev[e.Utg.Ks[j]]
				j++
			}
		}

		if !correct && errorNum > 0 {
			fmt.Printf("[MappingReadToEdgesBackWard]not perfect start i: %v,edge ID: %v,len(e.Utg.Ks):%v,  dk.Pos: %v, pos: %v, b: %v\n", i, dk.ID, len(e.Utg.Ks), dk.Pos, rpos, b)
			break
		}
		//fmt.Printf("[paraMapNGS2DBG] i: %v, j: %v, dk.Pos: %v, pos: %v, b: %v\n", i, j, dk.Pos, rpos, b)

		if i < 0 {
			ai.EndPos = j
			break
		}
		// find next edge
		rpos = i + 1
		var node DBGNode
		//var base byte
		// if is a self cycle edge
		if e.StartNID == e.EndNID {
			var pos int
			dk.ID, pos, strand = GetSelfCycleNextMapEdgeInfo(e.ID, nodesArr[e.StartNID], edgesArr, ai.Seq[rpos:rpos+(kmerlen-1)], kmerlen, BACKWARD, ri.Seq[i], correct)
			if dk.ID <= 1 {
				ai.EndPos = j
				ai.Seq = ai.Seq[rpos:]
				break
			}
			dk.Pos = pos
			continue
		}

		if strand == PLUS {
			if e.StartNID == 0 {
				break
			}
			node = nodesArr[e.StartNID]
			base := bnt.BntRev[ri.Seq[i]]
			if IsInComing(node.EdgeIDOutcoming, e.ID) && node.EdgeIDIncoming[ri.Seq[i]] > 1 {
				dk.ID = node.EdgeIDIncoming[ri.Seq[i]]
			} else if IsInComing(node.EdgeIDIncoming, e.ID) && node.EdgeIDOutcoming[base] > 1 {
				dk.ID = node.EdgeIDOutcoming[base]
			} else {
				if correct {
					ea := GetNearEdgeIDArr(node, e.ID)
					if len(ea) == 1 {
						dk.ID = ea[0]
					} else {
						ai.EndPos = j
						ai.Seq = ai.Seq[rpos:]
						fmt.Printf("[MappingReadToEdgesBackWard] not found next edge in node: %v\n", node)
						break
					}
				} else {
					fmt.Printf("[MappingReadToEdgesBackWard] not found next edge in node: %v\n", node)
					break
				}
			}
			ne := edgesArr[dk.ID]
			if ne.StartNID == ne.EndNID {
				nodeSeq := ai.Seq[rpos : rpos+(kmerlen-1)]
				if reflect.DeepEqual(nodeSeq, ne.Utg.Ks[len(ne.Utg.Ks)-(kmerlen-1):]) {
					dk.Pos = len(ne.Utg.Ks) - (kmerlen - 1)
					strand = PLUS
				} else if reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[:kmerlen-1])) {
					dk.Pos = kmerlen - 1
					strand = MINUS
				}
			} else {
				if ne.EndNID == node.ID {
					dk.Pos = len(ne.Utg.Ks) - (kmerlen - 1)
				} else {
					dk.Pos = kmerlen - 1
					strand = !strand
				}
			}
		} else { // strand == MINUS
			if e.EndNID == 0 {
				break
			}
			node = nodesArr[e.EndNID]
			base := bnt.BntRev[ri.Seq[i]]
			if IsInComing(node.EdgeIDIncoming, e.ID) && node.EdgeIDOutcoming[base] > 1 {
				dk.ID = node.EdgeIDOutcoming[base]
			} else if IsInComing(node.EdgeIDOutcoming, e.ID) && node.EdgeIDIncoming[ri.Seq[i]] > 1 {
				dk.ID = node.EdgeIDIncoming[ri.Seq[i]]
			} else {
				if correct {
					ea := GetNearEdgeIDArr(node, e.ID)
					if len(ea) == 1 {
						dk.ID = ea[0]
					} else {
						ai.EndPos = j
						ai.Seq = ai.Seq[rpos:]
						fmt.Printf("[MappingReadToEdgesBackWard] not found next edge in node: %v\n", node)
						break
					}
				} else {
					fmt.Printf("[MappingReadToEdgesBackWard] not found next edge in node: %v\n", node)
					break
				}
			}
			ne := edgesArr[dk.ID]
			if ne.StartNID == ne.EndNID {
				nodeSeq := ai.Seq[rpos : rpos+(kmerlen-1)]
				if reflect.DeepEqual(nodeSeq, ne.Utg.Ks[len(ne.Utg.Ks)-(kmerlen-1):]) {
					dk.Pos = len(ne.Utg.Ks) - (kmerlen - 1)
					strand = PLUS
				} else if reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[:kmerlen-1])) {
					dk.Pos = kmerlen - 1
					strand = MINUS
				}
			} else {
				if ne.StartNID == node.ID {
					dk.Pos = kmerlen - 1
				} else {
					dk.Pos = len(ne.Utg.Ks) - (kmerlen - 1)
					strand = !strand
				}
			}
		}
	}
	return
}*/

/*func MappingReadToEdgesForWard(dk DBGKmer, ri constructcf.ReadInfo, rpos int, rstrand bool, edgesArr []DBGEdge, nodesArr []DBGNode, Kmerlen int, correct bool) (errorNum int, ai AlignInfo) {
	var strand bool
	if dk.Strand == rstrand {
		dk.Pos += Kmerlen
		strand = PLUS
	} else {
		strand = MINUS
	}
	ai.Seq = make([]byte, len(ri.Seq))
	startPos := rpos - Kmerlen
	copy(ai.Seq[rpos-Kmerlen:rpos], ri.Seq[rpos-Kmerlen:rpos])
	ai.EndPos = math.MinInt32
	for i := rpos; i < len(ri.Seq); {
		e := edgesArr[dk.ID]
		ai.Paths = append(ai.Paths, e.ID)
		ai.Strands = append(ai.Strands, strand)
		//fmt.Printf("[MappingReadToEdgesForWard]ai: %v\n", ai)
		b := len(ri.Seq)
		var j int
		if strand == PLUS {
			if len(e.Utg.Ks)-int(dk.Pos) < len(ri.Seq)-rpos {
				b = rpos + (len(e.Utg.Ks) - int(dk.Pos))
			}
			j = int(dk.Pos)
			for ; i < b; i++ {
				//fmt.Printf("[MappingReadToEdgesForWard] i: %v, j: %v, dk.Pos: %v, pos: %v, b: %v\n", i, j, dk.Pos, rpos, b)
				if ri.Seq[i] != e.Utg.Ks[j] {
					errorNum++
					if !correct {
						break
					}
				}
				ai.Seq[i] = e.Utg.Ks[j]
				j++
			}
		} else { // strand == MINUS
			if len(ri.Seq)-rpos > int(dk.Pos) {
				b = rpos + int(dk.Pos)
			}
			j = int(dk.Pos) - 1
			for ; i < b; i++ {
				//fmt.Printf("[MappingReadToEdgesForWard] i: %v, j: %v, dk.Pos: %v, pos: %v, b: %v\n", i, j, dk.Pos, rpos, b)
				if ri.Seq[i] != bnt.BntRev[e.Utg.Ks[j]] {
					errorNum++
					if !correct {
						break
					}
				}
				ai.Seq[i] = bnt.BntRev[e.Utg.Ks[j]]
				j--
			}
		}

		if !correct && errorNum > 0 {
			fmt.Printf("[MappingReadToEdgesForWard]not perfect end i: %v,edge ID: %v,len(e.Utg.Ks): %v,  dk.Pos: %v, pos: %v, b: %v\n", i, dk.ID, len(e.Utg.Ks), dk.Pos, rpos, b)
			break
		}

		//fmt.Printf("[MappingReadToEdgesForWard]after alignment i: %v, j: %v, dk.Pos: %v, pos: %v, b: %v\n", i, j, dk.Pos, rpos, b)
		if i >= len(ri.Seq) {
			ai.EndPos = j
			ai.Seq = ai.Seq[startPos:]
			break
		}

		// find next edge
		rpos = i
		var node DBGNode
		var base byte
		// if is a self cycle edge
		if e.StartNID == e.EndNID {
			var pos int
			dk.ID, pos, strand = GetSelfCycleNextMapEdgeInfo(e.ID, nodesArr[e.StartNID], edgesArr, ai.Seq[rpos-(Kmerlen-1):rpos], Kmerlen, FORWARD, ri.Seq[i], correct)
			if dk.ID <= 1 {
				ai.EndPos = j
				break
			}
			dk.Pos = int(pos)
			continue
		}
		if strand == PLUS {
			if e.EndNID == 0 {
				break
			}
			node = nodesArr[e.EndNID]
			base = bnt.BntRev[ri.Seq[i]]
			if IsInComing(node.EdgeIDIncoming, e.ID) && node.EdgeIDOutcoming[ri.Seq[i]] > 1 {
				dk.ID = node.EdgeIDOutcoming[ri.Seq[i]]
			} else if IsInComing(node.EdgeIDOutcoming, e.ID) && node.EdgeIDIncoming[base] > 1 {
				dk.ID = node.EdgeIDIncoming[base]
			} else {
				if correct {
					ea := GetNearEdgeIDArr(node, e.ID)
					if len(ea) == 1 {
						dk.ID = ea[0]
					} else {
						ai.EndPos = j
						ai.Seq = ai.Seq[startPos:rpos]
						fmt.Printf("[MappingReadToEdgesForWard] not found next edge in node: %v\n", node)
						break
					}
				} else {
					fmt.Printf("[MappingReadToEdgesForWard] not found next edge in node: %v\n", node)
					break
				}
			}
			ne := edgesArr[dk.ID]
			if ne.StartNID == ne.EndNID {
				nodeSeq := ai.Seq[rpos-(Kmerlen-1) : rpos]
				if reflect.DeepEqual(nodeSeq, ne.Utg.Ks[:Kmerlen-1]) {
					dk.Pos = Kmerlen - 1
					strand = PLUS
				} else if reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[len(ne.Utg.Ks)-(Kmerlen-1):])) {
					dk.Pos = len(ne.Utg.Ks) - (Kmerlen - 1)
					strand = MINUS
				} else {
					log.Fatalf("[MappingReadToEdgesForWard] ne: %v not found proper start position\n", ne)
				}
			} else {
				if ne.StartNID == node.ID {
					dk.Pos = Kmerlen - 1
				} else {
					dk.Pos = len(ne.Utg.Ks) - (Kmerlen - 1)
					strand = !strand
				}
			}
		} else { // strand == MINUS
			if e.StartNID == 0 {
				break
			}
			node = nodesArr[e.StartNID]
			base = bnt.BntRev[ri.Seq[i]]
			if IsInComing(node.EdgeIDOutcoming, e.ID) && node.EdgeIDIncoming[base] > 1 {
				dk.ID = node.EdgeIDIncoming[base]
			} else if IsInComing(node.EdgeIDIncoming, e.ID) && node.EdgeIDOutcoming[ri.Seq[i]] > 1 {
				dk.ID = node.EdgeIDOutcoming[ri.Seq[i]]
			} else {
				if correct {
					ea := GetNearEdgeIDArr(node, e.ID)
					if len(ea) == 1 {
						dk.ID = ea[0]
					} else {
						ai.EndPos = j
						ai.Seq = ai.Seq[startPos:rpos]
						fmt.Printf("[MappingReadToEdgesForWard] not found next edge in node: %v\n", node)
						break
					}
				} else {
					fmt.Printf("[MappingReadToEdgesForWard] not found next edge in node: %v\n", node)
					break
				}
			}
			ne := edgesArr[dk.ID]
			if ne.StartNID == ne.EndNID {
				nodeSeq := ai.Seq[rpos-(Kmerlen-1) : rpos]
				if reflect.DeepEqual(nodeSeq, ne.Utg.Ks[:Kmerlen-1]) {
					dk.Pos = Kmerlen - 1
					strand = PLUS
				} else if reflect.DeepEqual(nodeSeq, GetReverseCompByteArr(ne.Utg.Ks[len(ne.Utg.Ks)-(Kmerlen-1):])) {
					dk.Pos = len(ne.Utg.Ks) - (Kmerlen - 1)
					strand = MINUS
				} else {
					log.Fatalf("[MappingReadToEdgesForWard] ne: %v not found proper start position\n", ne)
				}
			} else {
				if ne.EndNID == node.ID {
					dk.Pos = len(ne.Utg.Ks) - (Kmerlen - 1)
				} else {
					dk.Pos = Kmerlen - 1
					strand = !strand
				}
			}
		}
	}
	return
}*/

// parallel Map NGS reads to the DBG edges, then output alignment path for the DBG
/*func paraMapNGS2DBG(cs chan constructcf.ReadInfo, wc chan AlignInfo, nodesArr []DBGNode, edgesArr []DBGEdge, cf CuckooFilter, winSize int) {
	var notFoundSeedNum, mapOneEdgeNum, notPerfectNum int
	for {
		ri, ok := <-cs
		var ai AlignInfo
		if !ok {
			wc <- ai
			break
		}

		// found kmer seed position in the DBG edges
		dbgK, pos, strand := LocateSeedKmerCF(cf, ri, winSize, edgesArr)

		if dbgK.GetCount() == 0 { // not found in the cuckoofilter
			notFoundSeedNum++
			continue
		}

		// check can map two or more edges
		{
			el := len(edgesArr[dbgK.ID].Utg.Ks)
			if dbgK.Strand == strand {
				if dbgK.Pos > int(pos) && len(ri.Seq)-int(pos) < el-dbgK.Pos {
					mapOneEdgeNum++
					continue
				}
			} else {
				if int(pos) < el-(int(dbgK.Pos)+cf.Kmerlen) && len(ri.Seq)-(int(pos)+cf.Kmerlen) < int(dbgK.Pos) {
					mapOneEdgeNum++
					continue
				}
			}
		}

		// extend map to the DBG edges
		{
			// overAll := true // note if overall length read sequence match
			// map the start partition of read sequence
			errorNum, ar := MappingReadToEdgesBackWard(dbgK, ri, int(pos), strand, edgesArr, nodesArr, cf.Kmerlen, false)
			if errorNum > 0 {
				notPerfectNum++
				continue
			}
			ar.Paths = ReverseDBG_MAX_INTArr(ar.Paths)
			// map the end partition of read sequence
			errorNum, al := MappingReadToEdgesForWard(dbgK, ri, int(pos)+cf.Kmerlen, strand, edgesArr, nodesArr, cf.Kmerlen, false)

			if errorNum > 0 {
				//fmt.Printf("[paraMapNGS2DBG] ", a)
				notPerfectNum++
				continue
			}

			if len(ar.Paths) > 0 && len(al.Paths) > 0 && ar.Paths[len(ar.Paths)-1] == al.Paths[0] {
				ai.Paths = append(ar.Paths, al.Paths[1:]...)
				ai.ID = ri.ID
			} else {
				log.Fatalf("[paraMapNGS2DBG] ar: %v and al: %v not consis\n", ar, al)
			}

			// write to output
			if len(ai.Paths) > 1 {
				wc <- ai
			}
		}
	}
	fmt.Printf("[paraMapNGS2DBG] not found seed reads number is : %v\n", notFoundSeedNum)
	fmt.Printf("[paraMapNGS2DBG] map one edge reads number is : %v\n", mapOneEdgeNum)
	fmt.Printf("[paraMapNGS2DBG] not perfect mapping reads number is : %v\n", notPerfectNum)

	ai.ID = ri.ID
	e := edgesArr[dbgK.ID]
	var overAll bool // note if overall length match
	for i < len(ri.Seq)-k {
		var plus bool // if the read map to the edge plus strand
		var n DBGNode
		ek := e.Utg.Ks[dbgK.Pos : dbgK.Pos+uint32(k)]
		if reflect.DeepEqual(ri.Seq[i:i+k], ek) {
			plus = true
		} else if reflect.DeepEqual(ri.Seq[i:i+k], GetReverseCompByteArr(ek)) {
			plus = false
		} else { // not equal for the DBG edge
			log.Fatalf("[paraMapNGS2DBG] read kmer not equal to the DBG edge\nread info: %v\nedge info: %v\n", ri.Seq[i:i+k], ek)
		}
		//if output {
		//	fmt.Printf("[paraMapNGS2DBG] i : %v\tdbgK: %v\n\tread info: %v\n\tedge seq: %v\n\tedge info: ID: %v, len: %v, startNID: %v, endNID: %v\n", i, dbgK, ri.Seq[i:i+k], ek, e.ID, len(e.Utg.Ks), e.StartNID, e.EndNID)
		//}

		x := i + k
		if plus {
			y := int(dbgK.Pos) + k
			for ; y < len(e.Utg.Ks) && x < len(ri.Seq); y++ {
				if ri.Seq[x] != e.Utg.Ks[y] {
					break
				}
				x++
			}
			//if output {
			//	fmt.Printf("[paraMapNGS2DBG]plus i: %v,x: %v, dbgK: %v\n", i, x, dbgK)
			//}
			if x >= len(ri.Seq) {
				ai.Paths = append(ai.Paths, dbgK.ID)
				overAll = true
				break
			}
			if y < len(e.Utg.Ks) {
				break
			}
			ai.Paths = append(ai.Paths, dbgK.ID)
			// set next edge info
			if e.EndNID == 0 {
				break
			}
			n = nodesArr[e.EndNID]
			if IsInComing(n.EdgeIDIncoming, e.ID) {
				if n.EdgeIDOutcoming[ri.Seq[x]] > 0 {
					dbgK.ID = n.EdgeIDOutcoming[ri.Seq[x]]
				} else {
					break
				}
			} else {
				b := bnt.BntRev[ri.Seq[x]]
				//b := ri.Seq[x]
				if n.EdgeIDIncoming[b] > 0 {
					dbgK.ID = n.EdgeIDIncoming[b]
				} else {
					break
				}
			}

		} else { // if the strand as minus
			y := int(dbgK.Pos) - 1
			for ; y >= 0 && x < len(ri.Seq); y-- {
				b := bnt.BntRev[e.Utg.Ks[y]]
				if ri.Seq[x] != b {
					break
				}
				x++
			}
			//if output {
			//	fmt.Printf("[paraMapNGS2DBG]i: %v, x: %v, dbgK: %v\n", i, x, dbgK)
			//}
			if x >= len(ri.Seq) {
				ai.Paths = append(ai.Paths, dbgK.ID)
				break
			}
			if y >= 0 {
				break
			}
			ai.Paths = append(ai.Paths, dbgK.ID)
			// set next edge info
			if e.StartNID == 0 {
				break
			}
			n = nodesArr[e.StartNID]
			if IsInComing(n.EdgeIDOutcoming, e.ID) {
				//fmt.Printf("[paraMapNGS2DBG]x: %v, len(ri.Seq): %v\tri.Seq[x]: %v\n", x, len(ri.Seq), ri.Seq[x])
				b := bnt.BntRev[ri.Seq[x]]
				if n.EdgeIDIncoming[b] > 0 {
					dbgK.ID = n.EdgeIDIncoming[b]
				} else {
					break
				}
			} else {
				if n.EdgeIDOutcoming[ri.Seq[x]] > 0 {
					dbgK.ID = n.EdgeIDOutcoming[ri.Seq[x]]
				} else {
					break
				}
			}
		}

		e = edgesArr[dbgK.ID]
		if e.StartNID == n.ID {
			dbgK.Pos = 0
		} else {
			dbgK.Pos = uint32(len(e.Utg.Ks) - k)
		}
		i = x - k + 1
	}
}*/

/*func MapNGS2DBG(opt Options, nodesArr []DBGNode, edgesArr []DBGEdge, wrFn string) {
	// construct cuckoofilter of DBG sample
	cfSize := GetCuckoofilterDBGSampleSize(edgesArr, int64(opt.WinSize), int64(opt.MaxNGSReadLen), int64(opt.Kmer))
	fmt.Printf("[MapNGS2DBG] cfSize: %v\n", cfSize)
	cf := MakeCuckooFilter(uint64(cfSize*5), opt.Kmer)
	fmt.Printf("[MapNGS2DBG] cf.numItems: %v\n", cf.NumItems)
	count := ConstructCFDBGMinimizers(cf, edgesArr, opt.WinSize, int64(opt.MaxNGSReadLen))
	fmt.Printf("[MapNGS2DBG]construct Smaple of DBG edges cuckoofilter number is : %v\n", count)
	//if cfSize != int64(count) {
	//	log.Fatalf("[MapNGS2DBG]cfSize : %v != count : %v, please check\n", cfSize, count)
	//}

	numCPU := opt.NumCPU
	runtime.GOMAXPROCS(numCPU + 2)
	bufSize := 66000
	cs := make(chan constructcf.ReadInfo, bufSize)
	wc := make(chan AlignInfo, numCPU*20)
	defer close(wc)
	//defer close(cs)

	// Load NGS read from cfg
	fn := opt.CfgFn
	readT := (numCPU + 5 - 1) / 5
	go LoadNGSReads(fn, opt.Correct, cs, readT, opt.Kmer)
	for i := 0; i < numCPU; i++ {
		go paraMapNGS2DBG(cs, wc, nodesArr, edgesArr, cf, opt.WinSize)
	}

	// write function
	writeAlignToFile(wrFn, wc, numCPU)
}*/

func AtoiArr(sa []string) []DBG_MAX_INT {
	da := make([]DBG_MAX_INT, len(sa))
	for i, e := range sa {
		d, err := strconv.Atoi(e)
		if err != nil {
			log.Fatalf("[AtoiArr] string: %v convert to integer, err: %v\n", e, err)
		}
		da[i] = DBG_MAX_INT(d)
	}
	return da
}

// add to the DBGEdge pathMat
func AddPathToDBGEdge(edgesArr []DBGEdge, mapNGSFn string) {
	fp, err := os.Open(mapNGSFn)
	if err != nil {
		log.Fatalf("[AddPathToDBGEdge] open file: '%v' error, err : %v\n", mapNGSFn, err)
	}
	buffp := bufio.NewReader(fp)
	defer fp.Close()
	m, err := buffp.ReadString('\n')
	for ; err == nil; m, err = buffp.ReadString('\n') {
		sa := strings.Split(m[:len(m)-1], "\t")
		pa := strings.Split(sa[1], ":")
		da := AtoiArr(pa)
		//fmt.Printf("sa: %v\npa: %v\n", sa, pa)
		for i, eID := range da {
			// statistics coverage depth for every reads pass the start and end position
			cd := uint16(1)
			if 0 < i && i < len(da)-1 {
				cd = 2
			}
			if edgesArr[eID].CovD < math.MaxUint16 {
				edgesArr[eID].CovD += cd
			}
			if edgesArr[eID].GetUniqueFlag() > 0 || edgesArr[eID].GetSemiUniqueFlag() > 0 {
				edgesArr[eID].InsertPathToEdge(da, 1)
			}
		}
	}
	if err != io.EOF {
		log.Fatalf("[AddPathToDBGEdge] Failed to read file: %v, err : %v\n", mapNGSFn, err)
	}

	// at last edge average coverage = (start position coverage depth +  end position coverage depth)/2
	for i := 2; i < len(edgesArr); i++ {
		edgesArr[i].CovD /= 2
	}

}

func FindConsisPath(pID DBG_MAX_INT, e DBGEdge) (consisP Path) {
	var pm []Path
	for _, pa := range e.PathMat {
		p1 := IndexEID(pa.IDArr, pID)
		p2 := IndexEID(pa.IDArr, e.ID)
		if p1 >= 0 {
			var npa Path
			if p1 < p2 {
				npa.IDArr = GetReverseDBG_MAX_INTArr(pa.IDArr[:p2+1])
			} else {
				npa.IDArr = pa.IDArr[p2:]
			}
			npa.Freq = pa.Freq
			pm = append(pm, npa)
		}
	}

	// debug code
	for i, p := range pm {
		fmt.Printf("[FindConsisPath] pm[%v]: %v\n", i, p)
	}

	freq := 1
	for k := 0; freq > 0; k++ {
		freq = 0
		for _, p := range pm {
			if len(p.IDArr) <= k {
				continue
			}
			if len(consisP.IDArr) == k {
				consisP.IDArr = append(consisP.IDArr, p.IDArr[k])
			} else {
				if consisP.IDArr[k] != p.IDArr[k] {
					freq = 0
					consisP.IDArr = consisP.IDArr[:len(consisP.IDArr)-1] // remove last element
					break
				}
			}
			freq += p.Freq
		}
		if freq > 0 {
			consisP.Freq = freq
		}
	}
	return consisP
}

// coming denote node EdgeIDcoming, true is Outcoming, false is Incoming
func GetNearEdgeIDArr(nd DBGNode, eID DBG_MAX_INT, coming bool) (eArr []DBG_MAX_INT) {
	if eID <= 0 {
		log.Fatalf("[GetNearEdgeIDArr] eID must bigger than zero, eID: %v\n", eID)
	}
	if nd.ID < 2 {
		return
	}
	var ok bool
	if coming {
		for _, id := range nd.EdgeIDIncoming {
			if id == eID {
				ok = true
				break
			}
		}
	} else {
		for _, id := range nd.EdgeIDOutcoming {
			if id == eID {
				ok = true
				break
			}
		}
	}
	if !ok {
		log.Fatalf("[GetNearEdgeIDArr] coming: %v, not found eID: %v, in nd: %v\n", coming, eID, nd)
	}

	if coming {
		for _, id := range nd.EdgeIDOutcoming {
			if id > 1 {
				eArr = append(eArr, id)
			}
		}
	} else {
		for _, id := range nd.EdgeIDIncoming {
			if id > 1 {
				eArr = append(eArr, id)
			}
		}
	}

	return eArr
}

func FreqNumInDBG_MAX_INTArr(arr []DBG_MAX_INT, eID DBG_MAX_INT) (count int) {
	for _, id := range arr {
		if id == eID {
			count++
		}
	}

	return count
}

// merge DBGEdge's pathMat
/*func MergePathMat(edgesArr []DBGEdge, nodesArr []DBGNode, minMapFreq int) {
	for i, e := range edgesArr {
		//if e.GetUniqueFlag() > 0 {
		//	fmt.Printf("[MergePathMat]unique edge : %v\n", e)
		//}
		if i < 2 || e.GetDeleteFlag() > 0 || len(e.PathMat) == 0 || (e.GetUniqueFlag() == 0 && e.GetSemiUniqueFlag() == 0) {
			continue
		}

		// debug code
		if e.PathMat[0].Freq == 0 {
			log.Fatalf("[MergePathMat] e.ID: %v PathMat[0]: %v\n", e.ID, e.PathMat[0])
		}
		//fmt.Printf("[MergePathMat]e.PathMat : %v\n", e.PathMat)

		// check if is a node cycle edge
		if e.StartNID == e.EndNID {
			node := nodesArr[e.StartNID]
			n1, _ := GetEdgeIDComing(node.EdgeIDIncoming)
			n2, _ := GetEdgeIDComing(node.EdgeIDOutcoming)
			if n1 > 1 || n2 > 1 {
				edgesArr[e.ID].ResetUniqueFlag()
				edgesArr[e.ID].ResetSemiUniqueFlag()
			}
			continue
		}

		CheckPathDirection(edgesArr, nodesArr, e.ID)
		if edgesArr[e.ID].GetUniqueFlag() == 0 && edgesArr[e.ID].GetSemiUniqueFlag() == 0 {
			continue
		}
		if len(e.PathMat) == 1 {
			continue
		}

		// merge process
		var leftMax, rightMax int
		for _, p := range e.PathMat {
			idx := IndexEID(p.IDArr, e.ID)
			if idx > leftMax {
				leftMax = idx
			}
			if len(p.IDArr)-idx > rightMax {
				rightMax = len(p.IDArr) - idx
			}
		}
		al := leftMax + rightMax
		// copy e.PathMat to the pm
		pm := make([]Path, len(e.PathMat))
		for j, p := range e.PathMat {
			t := make([]DBG_MAX_INT, al)
			idx := IndexEID(p.IDArr, e.ID)
			copy(t[leftMax-idx:], p.IDArr)
			pm[j].Freq = p.Freq
			pm[j].IDArr = t
		}

		// alignment PathMat
		for j, p := range pm {
			fmt.Printf("[MergePathMat] pm[%v]: %v\n", j, p)
		}

		// find consis Path
		var path Path
		path.Freq = math.MaxInt
		// add left==0 and right==1 partition
		for z := 0; z < 2; z++ {
			var j, step int
			if z == 0 {
				j = leftMax
				step = -1
			} else {
				j = leftMax + 1
				step = 1
			}
			for ; ; j += step {
				if z == 0 {
					if j < 0 {
						break
					}
				} else {
					if j >= al {
						break
					}
				}
				suc := true // note found consis Path
				var freq int
				var id DBG_MAX_INT
				for k := 0; k < len(pm); k++ {
					if pm[k].IDArr[j] > 0 {
						if id == 0 {
							id = pm[k].IDArr[j]
							freq = pm[k].Freq
						} else {
							if id == pm[k].IDArr[j] {
								freq += pm[k].Freq
							} else {
								suc = false
								break
							}
						}
					}
				}
				if !suc {
					break
				}
				if freq >= minMapFreq {
					path.IDArr = append(path.IDArr, id)
					if path.Freq > freq {
						path.Freq = freq
					}
				} else {
					break
				}
			}

			if z == 0 {
				path.IDArr = GetReverseDBG_MAX_INTArr(path.IDArr)
			}
		}

		if path.Freq >= minMapFreq && len(path.IDArr) >= 2 {
			var pm []Path
			pm = append(pm, path)
			edgesArr[i].PathMat = pm
			fmt.Printf("[MergePathMat] edge ID: %v, merge path: %v\n", e.ID, edgesArr[i].PathMat)
		} else {
			edgesArr[i].PathMat = nil
		}
	}
}*/

func IsTwoEdgesCyclePath(edgesArr []DBGEdge, nodesArr []DBGNode, eID DBG_MAX_INT) bool {
	e := edgesArr[eID]
	if e.StartNID == 0 || e.EndNID == 0 {
		return false
	}
	var arr1, arr2 []DBG_MAX_INT
	if IsInComing(nodesArr[e.StartNID].EdgeIDIncoming, eID) {
		arr1 = GetNearEdgeIDArr(nodesArr[e.StartNID], eID, true)
	} else {
		arr1 = GetNearEdgeIDArr(nodesArr[e.StartNID], eID, false)
	}
	if IsInComing(nodesArr[e.EndNID].EdgeIDIncoming, eID) {
		arr2 = GetNearEdgeIDArr(nodesArr[e.EndNID], eID, true)
	} else {
		arr2 = GetNearEdgeIDArr(nodesArr[e.EndNID], eID, false)
	}
	if len(arr1) == 1 && len(arr2) == 1 && arr1[0] == arr2[0] {
		return true
	}

	return false
}

func ExtendPath(edgesArr []DBGEdge, nodesArr []DBGNode, e DBGEdge, minMappingFreq int, semi bool) (maxP Path) {
	var p Path
	p.IDArr = make([]DBG_MAX_INT, len(e.PathMat[0].IDArr))
	copy(p.IDArr, e.PathMat[0].IDArr)
	p.Freq = e.PathMat[0].Freq
	if p.Freq < minMappingFreq {
		return
	}
	idx := IndexEID(p.IDArr, e.ID)
	mutualArr := []DBG_MAX_INT{e.ID}
	fmt.Printf("[ExtendPath] e.ID: %v,e.PathMat[0]: %v\n", e.ID, e.PathMat[0])
	// found left partition path
	for i := 0; i < idx; i++ {
		e2 := edgesArr[p.IDArr[i]]
		//fmt.Printf("[ExtendPath] i: %v, idx: %v, e2.Process: %v, e2.Delete: %v, e2.Unique: %v\n", i, idx, e2.GetProcessFlag(), e2.GetDeleteFlag(), e2.GetUniqueFlag())
		if len(e2.PathMat) != 1 || e2.PathMat[0].Freq < minMappingFreq || e2.GetProcessFlag() > 0 || e2.GetDeleteFlag() > 0 {
			continue
		}
		if semi {
			if e2.GetUniqueFlag() == 0 && e2.GetSemiUniqueFlag() == 0 {
				continue
			}
		} else {
			if e2.GetUniqueFlag() == 0 {
				continue
			}
		}
		if IsInDBG_MAX_INTArr(mutualArr, e2.ID) {
			fmt.Printf("[ExtendPath] e2.ID: %v in the mutualArr: %v\n", e2.ID, mutualArr)
			continue
		}
		p2 := e2.PathMat[0]
		eID1 := mutualArr[len(mutualArr)-1]
		e1 := edgesArr[eID1]
		j1 := IndexEID(p2.IDArr, e1.ID)
		j2 := IndexEID(p2.IDArr, e2.ID)
		fmt.Printf("[ExtendPath]BACKWARD e1 ID: %v,  e2 ID: %v, p2: %v\n", e1.ID, e2.ID, p2)
		if j1 < 0 || j2 < 0 {
			continue
		}
		if j1 < j2 {
			p2.IDArr = GetReverseDBG_MAX_INTArr(p2.IDArr)
			j1 = IndexEID(p2.IDArr, e1.ID)
			//j2 = IndexEID(p2.IDArr, e2.ID)
		}
		k1 := IndexEID(p.IDArr, e1.ID)
		//fmt.Printf("[ExtendPath]j1: %v, j2:%v,k1: %v, eID1: %v, eID2: %v, p: %v, p2: %v\n", j1, j2, k1, e1.ID, e2.ID, p, p2)
		if j1 >= k1 && len(p2.IDArr)-j1 <= len(p.IDArr)-k1 {
			if reflect.DeepEqual(p2.IDArr[j1-k1:], p.IDArr[:len(p2.IDArr)-(j1-k1)]) {
				mutualArr = append(mutualArr, e2.ID)
				na := make([]DBG_MAX_INT, len(p.IDArr)+j1-k1)
				copy(na[:j1], p2.IDArr[:j1])
				copy(na[j1:], p.IDArr[k1:])
				p.IDArr = na
				if p2.Freq < p.Freq {
					p.Freq = p2.Freq
				}
				idx = IndexEID(p.IDArr, e2.ID)
				i = -1
			}
		}
		fmt.Printf("[ExtendPath] p: %v\n", p)
	}

	ReverseDBG_MAX_INTArr(mutualArr)
	idx = IndexEID(p.IDArr, e.ID)
	// find right path
	for i := len(p.IDArr) - 1; i > idx; i-- {
		e2 := edgesArr[p.IDArr[i]]
		if len(e2.PathMat) != 1 || e2.PathMat[0].Freq < minMappingFreq || e2.GetProcessFlag() > 0 || e2.GetDeleteFlag() > 0 {
			continue
		}
		if semi {
			if e2.GetUniqueFlag() == 0 && e2.GetSemiUniqueFlag() == 0 {
				continue
			}
		} else {
			if e2.GetUniqueFlag() == 0 {
				continue
			}
		}
		if IsInDBG_MAX_INTArr(mutualArr, e2.ID) {
			fmt.Printf("[ExtendPath] e2.ID: %v in the mutualArr: %v\n", e2.ID, mutualArr)
			continue
		}
		p2 := e2.PathMat[0]
		eID1 := mutualArr[len(mutualArr)-1]
		e1 := edgesArr[eID1]
		j1 := IndexEID(p2.IDArr, e1.ID)
		j2 := IndexEID(p2.IDArr, e2.ID)
		if j1 < 0 || j2 < 0 {
			continue
		}
		if j2 < j1 {
			p2.IDArr = GetReverseDBG_MAX_INTArr(p2.IDArr)
			j1 = IndexEID(p2.IDArr, e1.ID)
			//j2 = IndexEID(p2.IDArr, e2.ID)
		}
		k1 := IndexEID(p.IDArr, e1.ID)
		//fmt.Printf("[ExtendPath] j1: %v, j2: %v, k1: %v, eID1: %v, eID2: %v, p: %v, p2: %v\n", j1, j2, k1, e1.ID, e2.ID, p, p2)
		fmt.Printf("[ExtendPath]FORWARD e1 ID: %v,  e2 ID: %v, p2: %v\n", e1.ID, e2.ID, p2)
		if len(p2.IDArr)-j1 >= len(p.IDArr)-k1 && k1 >= j1 {
			if reflect.DeepEqual(p.IDArr[k1-j1:], p2.IDArr[:len(p.IDArr)-(k1-j1)]) {
				mutualArr = append(mutualArr, e2.ID)
				if len(p2.IDArr)-j1 > len(p.IDArr)-k1 {
					p.IDArr = append(p.IDArr, p2.IDArr[j1+(len(p.IDArr)-k1):]...)
				}
				if p2.Freq < p.Freq {
					p.Freq = p2.Freq
				}
				idx = IndexEID(p.IDArr, e2.ID)
				i = len(p.IDArr)
			}
		}
	}
	fmt.Printf("[ExtendPath] p: %v\n", p)

	// not allow two cycle edge in the start or end edge
	start, end := 0, len(mutualArr)-1
	for x, eID := range mutualArr {
		if edgesArr[eID].GetTwoEdgesCycleFlag() > 0 {
			start = x + 1
		} else {
			break
		}
	}
	for x := len(mutualArr) - 1; x > start; x-- {
		if edgesArr[mutualArr[x]].GetTwoEdgesCycleFlag() > 0 {
			end = x - 1
		} else {
			break
		}
	}
	if start >= end {
		edgesArr[e.ID].SetProcessFlag()
		return
	}
	mutualArr = mutualArr[start : end+1]

	// set maxP and process DBG
	i1 := IndexEID(p.IDArr, mutualArr[0])
	i2 := IndexEID(p.IDArr, mutualArr[len(mutualArr)-1])
	maxP.IDArr = p.IDArr[i1 : i2+1]
	maxP.Freq = p.Freq

	/*// set maxP paths subsequence by edge direction
	ce := edgesArr[maxP.IDArr[0]]
	if ce.StartNID > 0 {
		ea := GetNearEdgeIDArr(nodesArr[ce.StartNID], maxP.IDArr[0])
		if IsInDBG_MAX_INTArr(ea, maxP.IDArr[1]) {
			maxP.IDArr = GetReverseDBG_MAX_INTArr(maxP.IDArr)
		}
	}*/

	edgesArr[mutualArr[0]].SetProcessFlag()
	for _, id := range mutualArr[1:] {
		edgesArr[id].SetDeleteFlag()
		edgesArr[id].SetProcessFlag()
	}
	fmt.Printf("[ExtendPath] mutualArr: %v\n", mutualArr)
	fmt.Printf("[ExtendPath] maxP: %v\n", maxP)

	return
}

/* func ExtendPath(edgesArr []DBGEdge, nodesArr []DBGNode, e DBGEdge) (maxP Path) {
	maxP.IDArr = append(maxP.IDArr, e.PathMat[0].IDArr...)
	maxP.Freq = e.PathMat[0].Freq
	mutualArr := []DBG_MAX_INT{e.ID}
	fmt.Printf("[ExtendPath] edge info, eID: %v\t e.StartNID: %v\te.EndNID: %v\n", e.ID, e.StartNID, e.EndNID)
	if e.StartNID > 0 {
		nd := nodesArr[e.StartNID]
		ea := GetNearEdgeIDArr(nd, e.ID)
		fmt.Printf("[ExtendPath] ea: %v, maxP: %v, nd: %v\n", ea, maxP, nd)
		if len(ea) == 1 {
			maxP.IDArr = ReverseDBG_MAX_INTArr(maxP.IDArr)
			//p1 := IndexEID(maxP.IDArr, ea[0])
			p1 := IndexEID(maxP.IDArr, e.ID) + 1
			if p1 > 0 {
				for j := p1; j < len(maxP.IDArr); j++ {
					ne := edgesArr[maxP.IDArr[j]]
					var nextNID DBG_MAX_INT
					if ne.StartNID == nd.ID {
						nextNID = ne.EndNID
					} else {
						nextNID = ne.StartNID
					}
					if ne.GetDeleteFlag() > 0 || ne.GetUniqueFlag() == 0 || len(ne.PathMat) == 0 {
						nd = nodesArr[nextNID]
						continue
					}
					fmt.Printf("[ExtendPath] ne: %v\nne.PathMat[0]: %v\n", ne, ne.PathMat[0])
					var nP Path // next Path
					if ne.EndNID == nd.ID {
						nP.IDArr = GetReverseDBG_MAX_INTArr(ne.PathMat[0].IDArr)
					} else {
						nP.IDArr = make([]DBG_MAX_INT, len(ne.PathMat[0].IDArr))
						copy(nP.IDArr, ne.PathMat[0].IDArr)
					}
					nP.Freq = ne.PathMat[0].Freq
					fmt.Printf("[ExtendPath] eID1: %v, eID2: %v\n\tmaxP: %v\n\tnP: %v\n\tnd: %v\n", mutualArr[len(mutualArr)-1], ne.ID, maxP, nP, nd)
					if mutualReachable(maxP.IDArr, nP.IDArr, mutualArr[len(mutualArr)-1], ne.ID) {
						var suc bool
						maxP.IDArr, suc = FindConsistenceAndMergePath(maxP.IDArr, nP.IDArr, mutualArr[len(mutualArr)-1], ne.ID)
						if suc == false {
							log.Fatalf("[ExtendPath] Failed to merge two paths, maxP: %v\tnP: %v\n", maxP, nP)
						}
						mutualArr = append(mutualArr, ne.ID)
						if nP.Freq < maxP.Freq {
							maxP.Freq = nP.Freq
						}
						fmt.Printf("after merge, maxP: %v\n", maxP)
					}
					nd = nodesArr[nextNID]
				}
			}
			// reverse Array
			maxP.IDArr = ReverseDBG_MAX_INTArr(maxP.IDArr)
			mutualArr = ReverseDBG_MAX_INTArr(mutualArr)
		}
	}

	fmt.Printf("[ExtendPath] after extend previous edges, maxP: %v\n\te: %v\n", maxP, e)
	if e.EndNID > 0 {
		nd := nodesArr[e.EndNID]
		ea := GetNearEdgeIDArr(nd, e.ID)
		fmt.Printf("[ExtendPath] ea: %v, nd: %v\n", ea, nd)
		if len(ea) == 1 {
			//p1 := IndexEID(maxP.IDArr, ea[0])
			p1 := IndexEID(maxP.IDArr, e.ID) + 1
			if p1 > 0 {
				for j := p1; j < len(maxP.IDArr); j++ {
					ne := edgesArr[maxP.IDArr[j]]
					var nextNID DBG_MAX_INT
					if ne.StartNID == nd.ID {
						nextNID = ne.EndNID
					} else {
						nextNID = ne.StartNID
					}
					fmt.Printf("[ExtendPath] ne: %v\nne.PathMat: %v\n\tnd: %v\n", ne, ne.PathMat, nd)
					if ne.GetDeleteFlag() > 0 || ne.GetUniqueFlag() == 0 || len(ne.PathMat) == 0 {
						nd = nodesArr[nextNID]
						continue
					}
					var nP Path // next Path
					if ne.EndNID == nd.ID {
						nP.IDArr = GetReverseDBG_MAX_INTArr(ne.PathMat[0].IDArr)
					} else {
						nP.IDArr = make([]DBG_MAX_INT, len(ne.PathMat[0].IDArr))
						copy(nP.IDArr, ne.PathMat[0].IDArr)
					}
					nP.Freq = ne.PathMat[0].Freq
					fmt.Printf("[ExtendPath] eID1: %v, eID2: %v\n\tmaxP: %v\n\tnP: %v\n", mutualArr[len(mutualArr)-1], ne.ID, maxP, nP)
					if mutualReachable(maxP.IDArr, nP.IDArr, mutualArr[len(mutualArr)-1], ne.ID) {
						var suc bool
						maxP.IDArr, suc = FindConsistenceAndMergePath(maxP.IDArr, nP.IDArr, mutualArr[len(mutualArr)-1], ne.ID)
						if suc == false {
							log.Fatalf("[ExtendPath] Failed to merge two paths, maxP: %v\tne.PathMat[0]: %v\n", maxP, nP)
						}
						mutualArr = append(mutualArr, ne.ID)
						if nP.Freq < maxP.Freq {
							maxP.Freq = nP.Freq
						}
						fmt.Printf("after merge, maxP: %v\n", maxP)
					}
					nd = nodesArr[nextNID]
				}
			}
		}
	}

	// merge path and process DBG
	if len(mutualArr) == 1 {
		edgesArr[e.ID].SetProcessFlag()
		maxP.IDArr = nil
		maxP.Freq = 0
		//edgesArr[i].PathMat = nil
	} else {
		fmt.Printf("[ExtendPath]mutualArr: %v\t maxP: %v\n", mutualArr, maxP)
		eID1 := mutualArr[0]
		p1 := IndexEID(maxP.IDArr, eID1)
		eID2 := mutualArr[len(mutualArr)-1]
		p2 := IndexEID(maxP.IDArr, eID2)
		if IsTwoEdgesCyclePath(edgesArr, nodesArr, eID1) || IsTwoEdgesCyclePath(edgesArr, nodesArr, eID2) || len(maxP.IDArr[p1:p2+1]) <= 2 {
			maxP.IDArr = nil
			maxP.Freq = 0
			return maxP
		}
		//var np Path
		//np.IDArr = append(np.IDArr, maxP.IDArr[:p1+1]...)
		//np.IDArr = append(np.IDArr, maxP.IDArr[p2+1:]...)
		//np.Freq = maxP.Freq
		var np Path
		np.IDArr = append(np.IDArr, maxP.IDArr...)
		np.Freq = maxP.Freq
		edgesArr[eID1].PathMat = []Path{np}
		CheckPathDirection(edgesArr, nodesArr, eID1)
		maxP.IDArr = maxP.IDArr[p1 : p2+1]
		edgesArr[eID1].SetProcessFlag()
		edgesArr[eID2].SetProcessFlag()
		//edgesArr[eID].PathMat = []Path{maxP}
		//edgesArr[eID].SetProcessFlag()
		for _, id := range mutualArr[1 : len(mutualArr)-1] {
			//edgesArr[id].PathMat = nil
			edgesArr[id].SetDeleteFlag()
			edgesArr[id].SetProcessFlag()
		}
	}

	return maxP
} */

// find maximum path
func findMaxPath(edgesArr []DBGEdge, nodesArr []DBGNode, minMapFreq int, semi bool) (pathArr []Path) {
	for i, e := range edgesArr {
		if i < 2 || e.GetDeleteFlag() > 0 || e.GetProcessFlag() > 0 || len(e.PathMat) != 1 || e.GetTwoEdgesCycleFlag() > 0 {
			continue
		}

		if semi {
			if e.GetSemiUniqueFlag() == 0 {
				continue
			}
		} else {
			if e.GetUniqueFlag() == 0 {
				continue
			}
		}

		p := e.PathMat[0]
		if p.Freq < minMapFreq || len(p.IDArr) < 2 {
			//continue
			log.Fatalf("[findMaxPath] edgesArr[%v].PathMat: %v\t not contain useful info\n", i, p)
		}

		maxP := ExtendPath(edgesArr, nodesArr, e, minMapFreq, semi)
		if len(maxP.IDArr) > 1 {
			pathArr = append(pathArr, maxP)
		}

	}
	return pathArr
}

func GetLinkNodeID(p0, p1 DBGEdge) DBG_MAX_INT {
	if p0.StartNID == p1.StartNID || p0.StartNID == p1.EndNID {
		return p0.StartNID
	} else if p0.EndNID == p1.StartNID || p0.EndNID == p1.EndNID {
		return p0.EndNID
	} else {
		log.Fatalf("[GetLinkNodeID]not found link node ID p0: %v\n\tp1: %v\n", p0, p1)
	}
	return 0
}

func GetLinkPathArr(nodesArr []DBGNode, edgesArr []DBGEdge, nID DBG_MAX_INT) (p Path) {
	nd := nodesArr[nID]
	inNum, inID := GetEdgeIDComing(nd.EdgeIDIncoming)
	outNum, outID := GetEdgeIDComing(nd.EdgeIDOutcoming)
	if inNum != 1 || outNum != 1 {
		log.Fatalf("[GetLinkPathArr] inNum: %v or outNum: %v != 1\n", inNum, outNum)
	}
	nodesArr[nID].SetProcessFlag()
	arr := [2]DBG_MAX_INT{inID, outID}
	var dpArr [2][]DBG_MAX_INT
	for i, eID := range arr {
		dpArr[i] = append(dpArr[i], eID)
		id := edgesArr[eID].StartNID
		if id == nID {
			id = edgesArr[eID].EndNID
		}
		for id > 0 {
			nd := nodesArr[id]
			inNum, inID := GetEdgeIDComing(nd.EdgeIDIncoming)
			outNum, outID := GetEdgeIDComing(nd.EdgeIDOutcoming)
			if inNum == 1 && outNum == 1 {
				nodesArr[id].SetProcessFlag()
				if inID == eID {
					eID = outID
				} else {
					eID = inID
				}
				if IsTwoEdgesCyclePath(edgesArr, nodesArr, inID) || IsTwoEdgesCyclePath(edgesArr, nodesArr, outID) {
					break
				}
				dpArr[i] = append(dpArr[i], eID)
				if edgesArr[eID].StartNID == id {
					id = edgesArr[eID].EndNID
				} else {
					id = edgesArr[eID].StartNID
				}
			} else {
				break
			}
		}
	}
	p.IDArr = ReverseDBG_MAX_INTArr(dpArr[0])
	p.IDArr = append(p.IDArr, dpArr[1]...)

	return p
}

func CascadePath(p Path, edgesArr []DBGEdge, nodesArr []DBGNode, kmerlen int, changeDBG bool) DBGEdge {

	// keep self cycle path start node OUtcoming, end node Incoming
	e1, e2 := edgesArr[p.IDArr[0]], edgesArr[p.IDArr[1]]
	if e1.EndNID == e2.StartNID || e1.EndNID == e2.EndNID {
		if IsInComing(nodesArr[e1.StartNID].EdgeIDIncoming, p.IDArr[0]) {
			ReverseDBG_MAX_INTArr(p.IDArr)
		}
	} else {
		if IsInComing(nodesArr[e1.EndNID].EdgeIDIncoming, p.IDArr[0]) {
			ReverseDBG_MAX_INTArr(p.IDArr)
		}
	}

	p0 := edgesArr[p.IDArr[0]]
	p1 := edgesArr[p.IDArr[1]]
	//eStartNID, eEndNID := p0.StartNID, p0.EndNID
	var direction uint8
	nID := GetLinkNodeID(p0, p1)
	if p0.StartNID == nID {
		edgesArr[p.IDArr[0]].Utg = GetRCUnitig(edgesArr[p.IDArr[0]].Utg)
		//ReverseCompByteArr(edgesArr[p.IDArr[0]].Utg.Ks)
		//ReverseByteArr(edgesArr[p.IDArr[0]].Utg.Kq)
		edgesArr[p.IDArr[0]].StartNID, edgesArr[p.IDArr[0]].EndNID = edgesArr[p.IDArr[0]].EndNID, edgesArr[p.IDArr[0]].StartNID
		p0 = edgesArr[p.IDArr[0]]
	}
	if nID == p0.EndNID {
		direction = FORWARD
	} else {
		direction = BACKWARD
	}
	lastEID := p0.ID
	strand := true
	for _, eID := range p.IDArr[1:] {
		p0 = edgesArr[p0.ID]
		p1 = edgesArr[eID]
		//fmt.Printf("[CascadePath]p0.ID: %v\n\tp1.ID: %v, lastEID: %v, strand: %v, nID: %v\n", p0.ID, p1.ID, lastEID, strand,nID)
		//inID, outID := p0.ID, p1.ID
		//if direction == BACKWARD { inID, outID = outID, inID }
		//if IsInComing(nodesArr[nID].EdgeIDOutcoming, p1.ID) ==false &&  IsInComing(nodesArr[nID].EdgeIDIncoming, p1.ID) {
		//	inID, outID = outID, inID
		//}
		if direction == BACKWARD {
			if IsInComing(nodesArr[nID].EdgeIDOutcoming, lastEID) {
				if IsInComing(nodesArr[nID].EdgeIDIncoming, p1.ID) {
					if edgesArr[lastEID].StartNID == nID {
						if p1.EndNID != nID {
							strand = !strand
						}
					} else {
						if p1.StartNID != nID {
							strand = !strand
						}
					}
				} else {
					log.Fatalf("[CascadePath] nodesArr[%v]: %v\n\t, nID set error\n", nID, nodesArr[nID])
				}
			} else {
				if IsInComing(nodesArr[nID].EdgeIDOutcoming, p1.ID) {
					if edgesArr[lastEID].EndNID == nID {
						if p1.StartNID != nID {
							strand = !strand
						}
					} else {
						if p1.EndNID != nID {
							strand = !strand
						}
					}
				} else {
					log.Fatalf("[CascadePath]BACKWARD nodesArr[%v]: %v\n\t, nID set error\n", nID, nodesArr[nID])
				}
			}
			if !strand {
				p1.Utg = GetRCUnitig(p1.Utg)
			}
			fmt.Printf("[CascadePath]p0.ID: %v, p1.ID: %v, lastEID: %v, strand: %v, nID: Incoming: %v, Outcoming: %v\n", p0.ID, p1.ID, lastEID, strand, nodesArr[nID].EdgeIDIncoming, nodesArr[nID].EdgeIDOutcoming)
			edgesArr[p0.ID].Utg = ConcatEdges(p1.Utg, p0.Utg, kmerlen)
		} else {
			if IsInComing(nodesArr[nID].EdgeIDIncoming, lastEID) {
				if IsInComing(nodesArr[nID].EdgeIDOutcoming, p1.ID) {
					if edgesArr[lastEID].EndNID == nID {
						if p1.StartNID != nID {
							strand = !strand
						}
					} else {
						if p1.EndNID != nID {
							strand = !strand
						}
					}
				} else {
					log.Fatalf("[CascadePath] nodesArr[%v]: %v, nID set error\n", nID, nodesArr[nID])
				}
			} else {
				if IsInComing(nodesArr[nID].EdgeIDIncoming, p1.ID) {
					if edgesArr[lastEID].StartNID == nID {
						if p1.EndNID != nID {
							strand = !strand
						}
					} else {
						if p1.StartNID != nID {
							strand = !strand
						}
					}
				} else {
					log.Fatalf("[CascadePath] nodesArr[%v]: %v\n\t, nID set error\n", nID, nodesArr[nID])
				}
			}
			if !strand {
				p1.Utg = GetRCUnitig(p1.Utg)
			}
			fmt.Printf("[CascadePath]FORWARD p0.ID: %v, p1.ID: %v, lastEID: %v, strand: %v, nID: Incoming: %v, Outcoming: %v\n", p0.ID, p1.ID, lastEID, strand, nodesArr[nID].EdgeIDIncoming, nodesArr[nID].EdgeIDOutcoming)
			edgesArr[p0.ID].Utg = ConcatEdges(p0.Utg, p1.Utg, kmerlen)
		}

		if nID == edgesArr[p1.ID].StartNID {
			nID = edgesArr[p1.ID].EndNID
		} else {
			nID = edgesArr[p1.ID].StartNID
		}
		lastEID = p1.ID
	}

	if !changeDBG {
		return edgesArr[p0.ID]
	}

	if direction == FORWARD {
		if !SubstituteEdgeID(nodesArr, p0.EndNID, p0.ID, 0) {
			log.Fatalf("[ReconstructDBG] SubsitututeEdgeID for Merged edge error, node: %v\n\tedge:%v\n", nodesArr[p0.EndNID], p0)
		}
		if !SubstituteEdgeID(nodesArr, nID, p1.ID, p0.ID) {
			log.Fatalf("[ReconstructDBG] SubsitututeEdgeID for Merged edge error, node: %v\n\tedge:%v\n", nodesArr[nID], p1)
		}
		edgesArr[p0.ID].EndNID = nID
	} else {
		if !SubstituteEdgeID(nodesArr, p0.StartNID, p0.ID, 0) {
			log.Fatalf("[ReconstructDBG] SubsitututeEdgeID for Merged edge error, node: %v\n\tedge:%v\n", nodesArr[p0.StartNID], p0)
		}
		if !SubstituteEdgeID(nodesArr, nID, p1.ID, p0.ID) {
			log.Fatalf("[ReconstructDBG] SubsitututeEdgeID for Merged edge error, node: %v\n\tedge:%v\n", nodesArr[nID], p1)
		}
		edgesArr[p0.ID].StartNID = nID
	}
	return edgesArr[p0.ID]
}

// reset the nodesArr EdgeIDIncoming and EdgeIDOutcoming
func ResetDeleteEdgeIncoming(edgesArr []DBGEdge, nodesArr []DBGNode) {
	for _, e := range edgesArr {
		if e.ID < 2 || e.GetDeleteFlag() == 0 {
			continue
		}
		fmt.Printf("[ResetDeleteEdgeIncoming] deleted edge:%v\n", e)
		if e.StartNID > 0 {
			SubstituteEdgeID(nodesArr, e.StartNID, e.ID, 0)
			//log.Fatalf("[ResetDeleteEdgeIncoming] SubsitututeEdgeID for deleted edge error, nodesArr[%v]: %v\n\tedge:%v\n\tedge len: %v\n", e.StartNID, nodesArr[e.StartNID], e, e.GetSeqLen())
		}
		if e.EndNID > 0 {
			SubstituteEdgeID(nodesArr, e.EndNID, e.ID, 0)
			//log.Fatalf("[ResetDeleteEdgeIncoming] SubsitututeEdgeID for deleted edge error, nodesArr[%v]: %v\n\tedge:%v\n\tedge len: %v\n", e.EndNID, nodesArr[e.EndNID], e, e.GetSeqLen())
		}
	}
}

func ResetMergedEdgeNID(edgesArr []DBGEdge, nodesArr []DBGNode, joinPathArr []Path, IDMapPath map[DBG_MAX_INT]uint32, kmerlen int) {
	for _, e := range edgesArr {
		if e.ID == 0 {
			continue
		}
		if idx, ok := IDMapPath[e.ID]; ok {
			if e.ID == joinPathArr[idx].IDArr[0] {
				if e.StartNID > 0 {
					//SubstituteEdgeID(nodesArr, e.StartNID, e.ID, 0)
					if !IsInDBGNode(nodesArr[e.StartNID], e.ID) {
						//var sBnt constructcf.KmerBnt
						//sBnt.Seq = e.Utg.Ks[:kmerlen-1]
						ks := constructcf.GetReadBntKmer(e.Utg.Ks[:kmerlen-1], 0, kmerlen-1)
						ts := ks
						rs := constructcf.ReverseComplet(ks)
						if ks.BiggerThan(rs) {
							ks, rs = rs, ks
						}
						if reflect.DeepEqual(ks.Seq, nodesArr[e.StartNID].Seq) == false {
							log.Fatalf("[ResetMergedEdgeNID] ks != nodesArr[e.StartNID].Seq, ks: %v\n\tnodesArr[%v]: %v\n", ks, e.StartNID, nodesArr[e.StartNID])
						}
						c := e.Utg.Ks[kmerlen-1]
						if reflect.DeepEqual(ts, ks) {
							nodesArr[e.StartNID].EdgeIDOutcoming[c] = e.ID
						} else {
							nodesArr[e.StartNID].EdgeIDIncoming[bnt.BntRev[c]] = e.ID
						}
					}
				}
				if e.EndNID > 0 {
					//SubstituteEdgeID(nodesArr, e.EndNID, e.ID, 0)
					if !IsInDBGNode(nodesArr[e.EndNID], e.ID) {
						//var sBnt constructcf.ReadBnt
						//sBnt.Seq = e.Utg.Ks[e.GetSeqLen()-kmerlen+1:]
						ks := constructcf.GetReadBntKmer(e.Utg.Ks[e.GetSeqLen()-kmerlen+1:], 0, kmerlen-1)
						ts := ks
						rs := constructcf.ReverseComplet(ks)
						if ks.BiggerThan(rs) {
							ks, rs = rs, ks
						}
						if reflect.DeepEqual(ks.Seq, nodesArr[e.EndNID].Seq) == false {
							log.Fatalf("[ResetMergedEdgeNID] ks != nodesArr[e.StartNID].Seq, ks: %v\n\tnodesArr[%v]: %v\n", ks, e.StartNID, nodesArr[e.StartNID])
						}
						c := e.Utg.Ks[e.GetSeqLen()-kmerlen]
						if reflect.DeepEqual(ks, ts) {
							nodesArr[e.EndNID].EdgeIDIncoming[c] = e.ID
						} else {
							nodesArr[e.EndNID].EdgeIDOutcoming[bnt.BntRev[c]] = e.ID
						}
					}
				}
			}
		}
	}
}

func ResetNodeEdgecoming(edgesArr []DBGEdge, nodesArr []DBGNode) {
	for i, nd := range nodesArr {
		if nd.ID == 0 {
			continue
		}
		if nd.GetDeleteFlag() > 0 {
			nodesArr[i].ID = 0
			nodesArr[i].EdgeIDIncoming = [4]DBG_MAX_INT{0, 0, 0, 0}
			nodesArr[i].EdgeIDOutcoming = [4]DBG_MAX_INT{0, 0, 0, 0}
			nodesArr[i].Seq = nil
			continue
		}
		for j := 0; j < bnt.BaseTypeNum; j++ {
			if nd.EdgeIDIncoming[j] > 0 {
				eID := nd.EdgeIDIncoming[j]
				if edgesArr[eID].StartNID != nd.ID && edgesArr[eID].EndNID != nd.ID {
					nodesArr[i].EdgeIDIncoming[j] = 0
				}
			}
			if nd.EdgeIDOutcoming[j] > 0 {
				eID := nd.EdgeIDOutcoming[j]
				if edgesArr[eID].StartNID != nd.ID && edgesArr[eID].EndNID != nd.ID {
					nodesArr[i].EdgeIDOutcoming[j] = 0
				}
			}
		}
	}
}

func InitialDBGNodeProcessFlag(nodesArr []DBGNode) {
	for i, _ := range nodesArr {
		nodesArr[i].ResetProcessFlag()
	}
}

func ResetIDMapPath(IDMapPath map[DBG_MAX_INT]uint32, joinPathArr []Path, path Path) []Path {
	var np Path
	np.Freq = path.Freq
	joinIdx := -1
	for _, t := range path.IDArr {
		if idx, ok := IDMapPath[t]; ok {
			arr := joinPathArr[idx].IDArr
			if t == arr[len(arr)-1] {
				ReverseDBG_MAX_INTArr(arr)
			}
			np.IDArr = append(np.IDArr, arr...)
			delete(IDMapPath, t)
			if t == arr[0] {
				t = arr[len(arr)-1]
			} else {
				t = arr[0]
			}
			delete(IDMapPath, t)
			joinIdx = int(idx)
		} else {
			np.IDArr = append(np.IDArr, t)
		}
	}
	if joinIdx >= 0 {
		joinPathArr[joinIdx] = np
	} else {
		joinPathArr = append(joinPathArr, np)
		joinIdx = len(joinPathArr) - 1
	}
	IDMapPath[np.IDArr[0]] = uint32(joinIdx)
	IDMapPath[np.IDArr[len(np.IDArr)-1]] = uint32(joinIdx)

	return joinPathArr
}

func CleanEdgesArr(edgesArr []DBGEdge, nodesArr []DBGNode) (deleteNodeNum, deleteEdgeNum int) {
	for i, e := range edgesArr {
		if e.ID == 0 || e.GetDeleteFlag() > 0 || e.StartNID == 0 || e.EndNID == 0 {
			continue
		}
		var num int
		if IsInComing(nodesArr[e.StartNID].EdgeIDIncoming, e.ID) {
			arr := GetNearEdgeIDArr(nodesArr[e.StartNID], e.ID, true)
			num = len(arr)
		} else {
			arr := GetNearEdgeIDArr(nodesArr[e.StartNID], e.ID, false)
			num = len(arr)
		}
		if IsInComing(nodesArr[e.EndNID].EdgeIDIncoming, e.ID) {
			arr := GetNearEdgeIDArr(nodesArr[e.EndNID], e.ID, true)
			num += len(arr)
		} else {
			arr := GetNearEdgeIDArr(nodesArr[e.EndNID], e.ID, false)
			num += len(arr)
		}
		if num == 0 {
			deleteEdgeNum++
			edgesArr[i].SetDeleteFlag()
			fmt.Printf("[CleanEdgesArr]delete edge len: %v\t%v\n", len(edgesArr[i].Utg.Ks), edgesArr[i])
			if e.StartNID > 0 {
				nodesArr[e.StartNID].SetDeleteFlag()
				deleteNodeNum++
			}
			if e.EndNID > 0 {
				nodesArr[e.EndNID].SetDeleteFlag()
				deleteNodeNum++
			}
		}
	}

	return deleteNodeNum, deleteEdgeNum
}

func ReconstructDBG(edgesArr []DBGEdge, nodesArr []DBGNode, joinPathArr []Path, kmerlen int) {
	// join path that unique path
	for _, p := range joinPathArr {
		if len(p.IDArr) <= 1 {
			continue
		}
		fmt.Printf("[ReconstructDBG] p: %v\n", p)
		//if IsTwoEdgeCyclePath(path) { joinPathArr[i].IDArr = }

		CascadePath(p, edgesArr, nodesArr, kmerlen, true)
	}
	// Reset delete edges coming DBGNodes
	ResetDeleteEdgeIncoming(edgesArr, nodesArr)

	/*
		fmt.Printf("[ReconstructDBG] finished joinPathArr reconstruct\n")
		fmt.Printf("[ReconstructDBG] edgesArr[%v]: %v\n", 481, edgesArr[481])

		// reset the nodesArr EdgeIDIncoming and EdgeIDOutcoming
		ResetDeleteEdgeIncoming(edgesArr, nodesArr)
		fmt.Printf("[ReconstructDBG] after ResetDeleteEdgeIncoming edgesArr[%v]: %v\n", 481, edgesArr[481])
		ResetMergedEdgeNID(edgesArr, nodesArr, joinPathArr, IDMapPath)
		fmt.Printf("[ReconstructDBG] after ResetMergedEdgeNID edgesArr[%v]: %v\n", 481, edgesArr[481])

		// simplify DBG
		//var deleteNodeNum, deleteEdgeNum int
		// clean edgesArr
		deleteNodeNum, deleteEdgeNum := CleanEdgesArr(edgesArr, nodesArr)
		fmt.Printf("[ReconstructDBG] after CleanEdgesArr edgesArr[%v]: %v\n", 481, edgesArr[481])
		// clean nodesArr
		// initial nodesArr procesed Flag
		InitialDBGNodeProcessFlag(nodesArr)

		for i, nd := range nodesArr {
			if nd.ID == 0 || nd.GetDeleteFlag() > 0 || nd.GetProcessFlag() > 0 {
				continue
			}
			inNum, inID := GetEdgeIDComing(nd.EdgeIDIncoming)
			outNum, outID := GetEdgeIDComing(nd.EdgeIDOutcoming)
			if inNum == 0 && outNum == 0 {
				nodesArr[i].SetDeleteFlag()
				nodesArr[i].ID = 0
				deleteNodeNum++
			} else if inNum+outNum == 1 {
				id := inID
				if outNum == 1 {
					id = outID
				}
				if edgesArr[id].StartNID == nd.ID {
					edgesArr[id].StartNID = 0
				} else {
					edgesArr[id].EndNID = 0
				}
				deleteNodeNum++
			} else if inNum == 1 && outNum == 1 && inID != outID { // prevent cycle ring
				if edgesArr[inID].StartNID != nd.ID && edgesArr[inID].EndNID != nd.ID {
					edgesArr[inID].SetDeleteFlag()
					fmt.Printf("[ReconstructDBG] clean nodesArr deleted edge, edgesArr[%v]: %v\n\tnd: %v\n", inID, edgesArr[inID], nd)
					fmt.Printf("[ReconstructDBG] clean nodesArr deleted edge, edgesArr[%v]: %v\n", outID, edgesArr[outID])
					continue
				}
				if edgesArr[outID].StartNID != nd.ID && edgesArr[outID].EndNID != nd.ID {
					edgesArr[outID].SetDeleteFlag()
					fmt.Printf("[ReconstructDBG] clean nodesArr deleted edge, edgesArr[%v]: %v\n\tnd: %v\n", inID, edgesArr[inID], nd)
					fmt.Printf("[ReconstructDBG] clean nodesArr deleted edge, edgesArr[%v]: %v\n", outID, edgesArr[outID])
					continue
				}
				path := GetLinkPathArr(nodesArr, edgesArr, nd.ID)
				joinPathArr = ResetIDMapPath(IDMapPath, joinPathArr, path)
				fmt.Printf("[ReconstructDBG]clean nodesArr path: %v\n", path)

				for _, eID := range path.IDArr[1:] {
					edgesArr[eID].SetDeleteFlag()
				}
				// join path
				CascadePath(path, edgesArr, nodesArr)
			}
		}

		// reset the nodesArr EdgeIDIncoming and EdgeIDOutcoming
		ResetDeleteEdgeIncoming(edgesArr, nodesArr)
		ResetMergedEdgeNID(edgesArr, nodesArr, joinPathArr, IDMapPath)
		ResetNodeEdgecoming(edgesArr, nodesArr)

		fmt.Printf("[ReconstructDBG] delete edge number is : %v\n\tdelete node number is : %v\n\tmerge edge path number is : %v\n", deleteEdgeNum, deleteNodeNum, len(joinPathArr))
	*/
}

func findPathOverlap(jp Path, pathArr []DBG_MAX_INT, edgesArr []DBGEdge) (id DBG_MAX_INT, num int) {
	fmt.Printf("[findPathOverlap] jpArr: %v\tpathArr: %v\n", jp, pathArr)
	jpArr := jp.IDArr
	if jpArr[0] == pathArr[0] {
		i := 1
		for ; i < len(jpArr) && i < len(pathArr); i++ {
			if jpArr[i] != pathArr[i] {
				break
			}
		}
		if i == len(jpArr) || i == len(pathArr) {
			if edgesArr[jpArr[0]].GetDeleteFlag() == 0 {
				id = jpArr[0]
			} else if edgesArr[jpArr[len(jpArr)-1]].GetDeleteFlag() == 0 {
				id = jpArr[len(jpArr)-1]
			} else {
				for _, item := range jpArr[1 : len(jpArr)-1] {
					if edgesArr[item].GetDeleteFlag() == 0 {
						if id > 0 {
							log.Fatalf("[findPathOverlap] found more than one edge not deleted in the joinPathMat: %v\n", jpArr)
						}
						id = item
					}
				}
			}
			num = i
			return id, num
		}
	} else if jpArr[len(jpArr)-1] == pathArr[0] {
		rp := GetReverseDBG_MAX_INTArr(jpArr)
		i := 1
		fmt.Printf("[findPathOverlap] rp: %v\tpathArr: %v\n", rp, pathArr)
		for ; i < len(rp) && i < len(pathArr); i++ {
			if rp[i] != pathArr[i] {
				break
			}
		}
		if i == len(rp) || i == len(pathArr) {
			if edgesArr[rp[0]].GetDeleteFlag() == 0 {
				id = rp[0]
			} else if edgesArr[rp[len(rp)-1]].GetDeleteFlag() == 0 {
				id = rp[len(rp)-1]
			} else {
				for _, item := range rp[1 : len(rp)-1] {
					if edgesArr[item].GetDeleteFlag() == 0 {
						if id > 0 {
							log.Fatalf("[findPathOverlap] found more than one edge not deleted in the joinPathMat: %v\n", jpArr)
						}
						id = item
					}
				}
			}
			num = i
			return id, num
		}
	}

	return id, num
}

/*func CheckPathDirection(edgesArr []DBGEdge, nodesArr []DBGNode, eID DBG_MAX_INT) {
	e := edgesArr[eID]
	step := 1
	var ea1, ea2 []DBG_MAX_INT
	if e.StartNID > 0 {
		if IsInComing(nodesArr[e.StartNID].EdgeIDIncoming, eID) {
			ea1 = GetNearEdgeIDArr(nodesArr[e.StartNID], eID, true)
		} else {
			ea1 = GetNearEdgeIDArr(nodesArr[e.StartNID], eID, false)
		}
	}
	if e.EndNID > 0 {
		if IsInComing(nodesArr[e.EndNID].EdgeIDIncoming, eID) {
			ea2 = GetNearEdgeIDArr(nodesArr[e.EndNID], eID, true)
		} else {
			ea2 = GetNearEdgeIDArr(nodesArr[e.EndNID], eID, false)
		}
	}

	// found two edge cycle
	if e.GetTwoEdgesCycleFlag() > 0 {
		ea1 = GetNearEdgeIDArr(nodesArr[e.EndNID], ea1[0])
		ea2 = GetNearEdgeIDArr(nodesArr[e.StartNID], ea2[0])
		if len(ea1) == 2 && len(ea2) == 2 {
			if ea1[0] == e.ID {
				ea1[0] = ea1[1]
			}
			ea1 = ea1[:1]
			if ea2[0] == e.ID {
				ea2[0] = ea2[1]
			}
			ea2 = ea2[:1]
			step = 2
		} else {
			edgesArr[eID].ResetUniqueFlag()
			edgesArr[eID].ResetSemiUniqueFlag()
			return
		}
	} else if IsIntersection(ea1, ea2) {
		edgesArr[eID].ResetUniqueFlag()
		edgesArr[eID].ResetSemiUniqueFlag()
		return
	}

	for i, p := range e.PathMat {
		idx := IndexEID(p.IDArr, eID)
		if idx < len(p.IDArr)-step {
			if IsInDBG_MAX_INTArr(ea1, p.IDArr[idx+step]) {
				ReverseDBG_MAX_INTArr(edgesArr[eID].PathMat[i].IDArr)
			}
		} else {
			if idx-step >= 0 && IsInDBG_MAX_INTArr(ea2, p.IDArr[idx-step]) {
				ReverseDBG_MAX_INTArr(edgesArr[eID].PathMat[i].IDArr)
			}
		}
	}

	//fmt.Printf("[CheckPathDirection] ea1: %v, ea2: %v, PathMat: %v\n", ea1, ea2, edgesArr[eID].PathMat)

}*/

func AdjustEIDPath(e DBGEdge, joinPathArr []Path, IDMapPath map[DBG_MAX_INT]uint32) (p Path) {
	ep := e.PathMat[0]
	idx := IndexEID(ep.IDArr, e.ID)
	p.Freq = ep.Freq
	fmt.Printf("[AdjustEIDPath] e: %v\n", e)
	if x, ok := IDMapPath[e.ID]; ok {
		a1, a2 := ep.IDArr, joinPathArr[x].IDArr
		fmt.Printf("[AdjustEIDPath] ep: %v\n\tjoinPathArr: %v\n", ep, joinPathArr[x])
		if idx < len(a1)-1 && a1[idx] == a2[0] && a1[idx+1] == a2[1] {
			l := len(a2)
			if l > len(a1[idx:]) {
				l = len(a1[idx:])
			}
			if reflect.DeepEqual(a1[idx:idx+l], a2[:l]) {
				p.IDArr = append(p.IDArr, a1[:idx+1]...)
				//p.IDArr = append(p.IDArr, e.ID)
				p.IDArr = append(p.IDArr, a1[idx+l:]...)
			}
		} else if idx < len(a1)-1 && a1[idx] == a2[len(a2)-1] && a1[idx+1] == a2[len(a2)-2] {
			a2 = GetReverseDBG_MAX_INTArr(a2)
			l := len(a2)
			if l > len(a1[idx:]) {
				l = len(a1[idx:])
			}
			if reflect.DeepEqual(a1[idx:idx+l], a2[:l]) {
				p.IDArr = append(p.IDArr, a1[:idx+1]...)
				//p.IDArr = append(p.IDArr, e.ID)
				p.IDArr = append(p.IDArr, a1[idx+l:]...)
			}
		} else if idx > 0 && a1[idx] == a2[0] && a1[idx-1] == a2[1] {
			a2 := GetReverseDBG_MAX_INTArr(a2)
			l := len(a2)
			if l > len(a1[:idx+1]) {
				l = len(a1[:idx+1])
			}
			if reflect.DeepEqual(a1[idx-l+1:idx+1], a2[len(a2)-l:]) {
				p.IDArr = append(p.IDArr, a1[:idx-l+1]...)
				p.IDArr = append(p.IDArr, e.ID)
				p.IDArr = append(p.IDArr, a1[idx+1:]...)
			}
		} else if idx > 0 && a1[idx] == a2[len(a2)-1] && a1[idx-1] == a2[len(a2)-2] {
			l := len(a2)
			if l > len(a1[:idx+1]) {
				l = len(a1[:idx+1])
			}
			if reflect.DeepEqual(a1[idx+1-l:idx+1], a2[len(a2)-l:]) {
				p.IDArr = append(p.IDArr, a1[:idx-l+1]...)
				p.IDArr = append(p.IDArr, e.ID)
				p.IDArr = append(p.IDArr, a1[idx+1:]...)
			}
		}
		/*else {
			log.Fatalf("[AdjustEIDPath] not found overlap Path, eID: %v, ep: %v\n\tjoinPathArr: %v\n", e.ID, a1, a2)
		}*/
	}
	if len(p.IDArr) == 0 {
		p.IDArr = ep.IDArr
	}
	fmt.Printf("[AdjustEIDPath] p: %v\n", p)
	return p
}

/*func AdjustPathMat(edgesArr []DBGEdge, nodesArr []DBGNode, joinPathArr []Path, IDMapPath map[DBG_MAX_INT]uint32) {

	// adjust pathMat
	for i, e := range edgesArr {
		if e.ID == 0 || e.GetDeleteFlag() > 0 || len(e.PathMat) == 0 {
			continue
		}
		if IsTwoEdgesCyclePath(edgesArr, nodesArr, e.ID) {
			continue
		}
		edgesArr[i].PathMat[0] = AdjustEIDPath(edgesArr[i], joinPathArr, IDMapPath)
		CheckPathDirection(edgesArr, nodesArr, e.ID)
		e = edgesArr[i]
		p := e.PathMat[0]
		fmt.Printf("[AdjustPathMat] e.PathMat: %v\n", e.PathMat)
		idx := IndexEID(p.IDArr, e.ID)
		arr := []DBG_MAX_INT{e.ID}
		if e.StartNID > 0 && idx > 0 {
			nd := nodesArr[e.StartNID]
			te := e
			rp := GetReverseDBG_MAX_INTArr(p.IDArr[:idx])
			for j := 0; j < len(rp); {
				eID := rp[j]
				ea := GetNearEdgeIDArr(nd, te.ID)
				if idx, ok := IDMapPath[eID]; ok {
					id, num := findPathOverlap(joinPathArr[idx], rp[j:], edgesArr)
					if id == 0 {
						log.Fatalf("[AdjustPathMat] not found overlap Path, eID: %v, p: %v\n\trp: %v\n", eID, joinPathArr[idx], rp)
					} else {
						arr = append(arr, id)
						j += num
					}
				} else {
					if IsInDBG_MAX_INTArr(ea, eID) {
						arr = append(arr, eID)
						j++
					} else {
						log.Fatalf("[AdjustPathMat]not found joinPathArr, edge ID: %v, PathMat: %v, eID: %v\n", e.ID, p, eID)
					}
				}
				te = edgesArr[arr[len(arr)-1]]
				if te.StartNID == nd.ID {
					nd = nodesArr[te.EndNID]
				} else {
					nd = nodesArr[te.StartNID]
				}
			}
			ReverseDBG_MAX_INTArr(arr)
		}

		if e.EndNID > 0 && idx < len(p.IDArr)-1 {
			nd := nodesArr[e.EndNID]
			te := e
			tp := p.IDArr[idx+1:]
			for j := 0; j < len(tp); {
				eID := tp[j]
				ea := GetNearEdgeIDArr(nd, te.ID)
				if idx, ok := IDMapPath[eID]; ok {
					id, num := findPathOverlap(joinPathArr[idx], tp[j:], edgesArr)
					if id == 0 {
						log.Fatalf("[AdjustPathMat] not found overlap Path, eID: %v, p: %v\n\ttp: %v\n", eID, joinPathArr[idx], tp)
					} else {
						arr = append(arr, id)
						j += num
					}
				} else {
					if IsInDBG_MAX_INTArr(ea, eID) {
						arr = append(arr, eID)
						j++
					} else {
						log.Fatalf("[AdjustPathMat] not found joinPathArr and nd: %v, edge ID: %v, PathMat: %v, eID: %v\n", nd, e.ID, p, eID)
					}
				}
				te = edgesArr[arr[len(arr)-1]]
				if te.StartNID == nd.ID {
					nd = nodesArr[te.EndNID]
				} else {
					nd = nodesArr[te.StartNID]
				}
			}
		}
		var np Path
		np.Freq = p.Freq
		np.IDArr = arr
		edgesArr[i].PathMat[0] = np
		fmt.Printf("[AdjustPathMat] before adjust path: %v\n\tafter path: %v\n", p, np)
	}
}*/

func IsContainBoundaryArr(a1, a2 []DBG_MAX_INT) bool {
	if reflect.DeepEqual(a1[:len(a2)], a2) {
		return true
	}
	if reflect.DeepEqual(a1[len(a1)-len(a2):], a2) {
		return true
	}
	ra2 := GetReverseDBG_MAX_INTArr(a2)
	if reflect.DeepEqual(a1[:len(ra2)], ra2) {
		return true
	}
	if reflect.DeepEqual(a1[len(a1)-len(ra2):], ra2) {
		return true
	}
	return false
}

// constuct map edge ID to the path
func ConstructIDMapPath(joinPathArr []Path) map[DBG_MAX_INT]uint32 {
	IDMapPath := make(map[DBG_MAX_INT]uint32)
	for i, p := range joinPathArr {
		sc := [2]DBG_MAX_INT{p.IDArr[0], p.IDArr[len(p.IDArr)-1]}
		for _, id := range sc {
			if idx, ok := IDMapPath[id]; ok {
				a := joinPathArr[idx]
				log.Fatalf("[ConstructIDMapPath] path: %v collison with : %v\n", a, p)
			} else {
				IDMapPath[id] = uint32(i)
			}
			/*fmt.Printf("[ConstructIDMapPath] path: %v collison with : %v\n", a, p)
			a1, a2 := p, a
			if len(a.IDArr) > len(p.IDArr) {
				a1, a2 = a, p
			}
			if IsContainBoundaryArr(a1.IDArr, a2.IDArr) {
				if len(a1.IDArr) > len(a.IDArr) {
					fmt.Printf("[ConstructIDMapPath] delete : %v\n", joinPathArr[idx])
					delete(IDMapPath, joinPathArr[idx].IDArr[0])
					delete(IDMapPath, joinPathArr[idx].IDArr[len(joinPathArr[idx].IDArr)-1])
					joinPathArr[idx].IDArr = nil
					joinPathArr[idx].Freq = 0
					IDMapPath[id] = uint32(i)
				} else {
					joinPathArr[i].IDArr = nil
					joinPathArr[i].Freq = 0
					if j == 0 {
						break
					} else {
						delete(IDMapPath, sc[0])
					}
				}
			} else {
				log.Fatalf("[ConstructIDMapPath] path: %v collison with : %v\n", a, p)
			} */

		}
	}

	return IDMapPath
}

func DeleteJoinPathArrEnd(edgesArr []DBGEdge, joinPathArr []Path) {
	for _, p := range joinPathArr {
		if p.Freq > 0 && len(p.IDArr) > 0 {
			eID := p.IDArr[len(p.IDArr)-1]
			edgesArr[eID].SetDeleteFlag()
		}
	}
}

/*func SimplifyByNGS(opt Options, nodesArr []DBGNode, edgesArr []DBGEdge, mapNGSFn string) {
	// add to the DBGEdge pathMat
	AddPathToDBGEdge(edgesArr, mapNGSFn)

	// merge pathMat
	MergePathMat(edgesArr, nodesArr, opt.MinMapFreq)

	// find maximum path
	joinPathArr := findMaxPath(edgesArr, nodesArr, opt.MinMapFreq, false) // just used for unique edge
	joinPathArr1 := findMaxPath(edgesArr, nodesArr, opt.MinMapFreq, true) // just used for semi-unique edge
	fmt.Printf("[SimplifyByNGS] findMaxPath number of the uinque edges : %v\n", len(joinPathArr))
	fmt.Printf("[SimplifyByNGS] findMaxPath number of  the semi-uinque edges : %v\n", len(joinPathArr1))
	// ReconstructDBG must first reconstruct uinque edges path , then process semi-unique edges,
	// because semi-unique path maybe been contained in the uinque path,
	// that will cause collison when  ReconstructDBG()
	joinPathArr = append(joinPathArr, joinPathArr1...)
	i := 125
	if edgesArr[i].GetDeleteFlag() == 0 {
		log.Fatalf("[SimplifyByNGS]edgesArr[%v]: %v\n", i, edgesArr[i])
	}
	// constuct map edge ID to the path
	//DeleteJoinPathArrEnd(edgesArr, joinPathArr)
	ReconstructDBG(edgesArr, nodesArr, joinPathArr, opt.Kmer)
	// debug code
	//graphfn := opt.Prefix + ".afterNGS.dot"
	//GraphvizDBGArr(nodesArr, edgesArr, graphfn)

	//AdjustPathMat(edgesArr, nodesArr, joinPathArr, IDMapPath)
}*/

func CheckInterConnectivity(edgesArr []DBGEdge, nodesArr []DBGNode) {
	for _, e := range edgesArr {
		if e.ID < 2 || e.GetDeleteFlag() > 0 {
			continue
		}
		if e.StartNID > 0 && (nodesArr[e.StartNID].GetDeleteFlag() > 0 || !IsInDBGNode(nodesArr[e.StartNID], e.ID)) {
			log.Fatalf("[CheckInterConnectivity]edge check nd: %v\n\tedge: %v\n", nodesArr[e.StartNID], e)
		}
		if e.EndNID > 0 && (nodesArr[e.EndNID].GetDeleteFlag() > 0 || !IsInDBGNode(nodesArr[e.EndNID], e.ID)) {
			log.Fatalf("[CheckInterConnectivity]edge check nd: %v\n\tedge: %v\n", nodesArr[e.EndNID], e)
		}
	}

	for _, nd := range nodesArr {
		if nd.ID == 0 || nd.GetDeleteFlag() > 0 {
			continue
		}

		for j := 0; j < bnt.BaseTypeNum; j++ {
			if nd.EdgeIDIncoming[j] > 1 {
				eID := nd.EdgeIDIncoming[j]
				if edgesArr[eID].GetDeleteFlag() > 0 || (edgesArr[eID].StartNID != nd.ID && edgesArr[eID].EndNID != nd.ID) {
					log.Fatalf("[CheckInterConnectivity]node check nd: %v\n\tedge: %v\n", nd, edgesArr[eID])
				}
			}
			if nd.EdgeIDOutcoming[j] > 1 {
				eID := nd.EdgeIDOutcoming[j]
				if edgesArr[eID].GetDeleteFlag() > 0 || (edgesArr[eID].StartNID != nd.ID && edgesArr[eID].EndNID != nd.ID) {
					log.Fatalf("[CheckInterConnectivity]node check nd: %v\n\tedge: %v\n", nd, edgesArr[eID])
				}
			}
		}
	}
}

type Options struct {
	utils.ArgsOpt
	TipMaxLen     int
	WinSize       int
	MaxNGSReadLen int
	MinMapFreq    int
	Correct       bool
	//MaxMapEdgeLen int // max length of edge that don't need cut two flank sequence to map Long Reads
}

func checkArgs(c cli.Command) (opt Options, succ bool) {

	var ok bool
	opt.TipMaxLen, ok = c.Flag("tipMaxLen").Get().(int)
	if !ok {
		log.Fatalf("[checkArgs] argument 'tipMaxLen': %v set error\n ", c.Flag("tipMaxlen").String())
	}
	//opt.TipMaxLen = tmp

	//tmp, err = strconv.Atoi(c.Flag("WinSize").String())
	//tmp = c.Flag("WinSize")
	opt.WinSize, ok = c.Flag("WinSize").Get().(int)
	if !ok {
		log.Fatalf("[checkArgs] argument 'WinSize': %v set error\n ", c.Flag("WinSize").String())
	}
	if opt.WinSize < 1 || opt.WinSize > 100 {
		log.Fatalf("[checkArgs] argument 'WinSize': %v must between 1~100\n", c.Flag("WinSize"))
	}
	opt.MaxNGSReadLen, ok = c.Flag("MaxNGSReadLen").Get().(int)
	if !ok {
		log.Fatalf("[checkArgs] argument 'MaxNGSReadLen': %v set error\n ", c.Flag("MaxNGSReadLen").String())
	}
	if opt.MaxNGSReadLen < opt.Kmer+50 {
		log.Fatalf("[checkArgs] argument 'MaxNGSReadLen': %v must bigger than K+50\n", c.Flag("MaxNGSReadLen").String())
	}

	opt.MinMapFreq, ok = c.Flag("MinMapFreq").Get().(int)
	if !ok {
		log.Fatalf("[checkArgs] argument 'MinMapFreq': %v set error\n ", c.Flag("MinMapFreq").String())
	}
	if opt.MinMapFreq < 5 && opt.MinMapFreq >= 20 {
		log.Fatalf("[checkArgs] argument 'MinMapFreq': %v must 5 <= MinMapFreq < 20\n", c.Flag("MinMapFreq").String())
	}

	opt.Correct, ok = c.Flag("Correct").Get().(bool)
	if !ok {
		log.Fatalf("[checkArgs] argument 'Correct': %v set error\n ", c.Flag("Correct").String())
	}

	if opt.TipMaxLen == 0 {
		opt.TipMaxLen = opt.MaxNGSReadLen
	}

	/*opt.MaxMapEdgeLen, ok = c.Flag("MaxMapEdgeLen").Get().(int)
	if !ok {
		log.Fatalf("[checkArgs] argument 'MaxMapEdgeLen': %v set error\n ", c.Flag("MaxMapEdgeLen").String())
	}
	if opt.MaxMapEdgeLen < 2000 {
		log.Fatalf("[checkArgs] argument 'MaxMapEdgeLen': %v must bigger than 2000\n", c.Flag("MaxMapEdgeLen").String())
	}*/

	succ = true
	return opt, succ
}

func Smfy(c cli.Command) {

	t0 := time.Now()
	// check agruments
	gOpt, suc := utils.CheckGlobalArgs(c.Parent())
	if suc == false {
		log.Fatalf("[Smfy] check global Arguments error, opt: %v\n", gOpt)
	}
	opt := Options{gOpt, 0, 0, 0, 0, false}
	tmp, suc := checkArgs(c)
	if suc == false {
		log.Fatalf("[Smfy] check Arguments error, opt: %v\n", tmp)
	}
	opt.TipMaxLen = tmp.TipMaxLen
	opt.MaxNGSReadLen = tmp.MaxNGSReadLen
	opt.WinSize = tmp.WinSize
	opt.MinMapFreq = tmp.MinMapFreq
	opt.Correct = tmp.Correct
	//opt.MaxMapEdgeLen = tmp.MaxMapEdgeLen
	fmt.Printf("Arguments: %v\n", opt)

	// set package-level variable
	//Kmerlen = opt.Kmer

	// read nodes file and transform to array mode for more quckly access
	nodesfn := opt.Prefix + ".nodes.mmap"
	nodeMap := NodeMapMmapReader(nodesfn)
	DBGStatfn := opt.Prefix + ".DBG.stat"
	nodesSize, edgesSize := DBGStatReader(DBGStatfn)
	//nodesSize := len(nodeMap)
	fmt.Printf("[Smfy] len(nodeMap): %v, length of edge array: %v\n", nodesSize, edgesSize)
	// read edges file
	edgesfn := opt.Prefix + ".edges.fq"
	edgesArr := ReadEdgesFromFile(edgesfn, edgesSize)
	gfn1 := opt.Prefix + ".beforeSmfyDBG.dot"
	GraphvizDBG(nodeMap, edgesArr, gfn1)

	//gfn := opt.Prefix + ".smfyDBG.dot"
	//GraphvizDBG(nodeMap, edgesArr, gfn)
	// reconstruct consistence De Bruijn Graph
	// ReconstructConsistenceDBG(nodeMap, edgesArr)

	nodesArr := make([]DBGNode, nodesSize)
	NodeMap2NodeArr(nodeMap, nodesArr)
	nodeMap = nil // nodeMap any more used

	t1 := time.Now()
	SmfyDBG(nodesArr, edgesArr, opt)
	MakeSelfCycleEdgeOutcomingToIncoming(nodesArr, edgesArr, opt)
	// set the unique edge of edgesArr
	uniqueNum, semiUniqueNum, twoEdgeCycleNum, selfCycleNum := SetDBGEdgesUniqueFlag(edgesArr, nodesArr)
	//CheckInterConnectivity(edgesArr, nodesArr)
	fmt.Printf("[Smfy] the number of DBG Unique  Edges is : %d\n", uniqueNum)
	fmt.Printf("[Smfy] the number of DBG Semi-Unique  Edges is : %d\n", semiUniqueNum)
	fmt.Printf("[Smfy] the number of DBG twoEdgeCycleNum  Edges is : %d\n", twoEdgeCycleNum)
	fmt.Printf("[Smfy] the number of DBG selfCycleNum  Edges is : %d\n", selfCycleNum)

	// map Illumina reads to the DBG and find reads map path for simplify DBG
	/*wrFn := opt.Prefix + ".smfy.NGSAlignment"
	MapNGS2DBG(opt, nodesArr, edgesArr, wrFn)
	//CheckInterConnectivity(edgesArr, nodesArr)
	SimplifyByNGS(opt, nodesArr, edgesArr, wrFn)
	SmfyDBG(nodesArr, edgesArr, opt)
	*/
	CheckInterConnectivity(edgesArr, nodesArr)
	// simplify DBG
	//IDMapPath := ConstructIDMapPath(joinPathArr)
	//AdjustPathMat(edgesArr, nodesArr, joinPathArr, IDMapPath)

	t2 := time.Now()
	fmt.Printf("[Smfy] total used : %v, MapNGS2DBG used : %v\n", t2.Sub(t0), t2.Sub(t1))
	graphfn := opt.Prefix + ".afterSmfy.dot"
	GraphvizDBGArr(nodesArr, edgesArr, graphfn)

	// Debug code
	// for i := 1; i < len(edgesArr); i++ {
	// 	fmt.Printf("[Smfy]edgesArr[%d]: %v\n", i, edgesArr[i])
	// 	if len(edgesArr[i].Utg.Ks) == len(edgesArr[i-1].Utg.Ks) {
	// 		fmt.Printf("[Smfy] outcoming: %v, %v, len: %d\n", edgesArr[i-1].Utg.Ks[Kmerlen-1], edgesArr[i].Utg.Ks[Kmerlen-1], len(edgesArr[i].Utg.Ks))
	// 	}
	// }
	// Debug code
	// output graphviz graph

	smfyEdgesfn := opt.Prefix + ".edges.smfy.fq"
	StoreEdgesToFn(smfyEdgesfn, edgesArr)
	//mappingEdgefn := opt.Prefix + ".edges.mapping.fa"
	// StoreMappingEdgesToFn(mappingEdgefn, edgesArr, opt.MaxMapEdgeLen)
	//	adpaterEdgesfn := prefix + ".edges.adapter.fq"
	//	StoreEdgesToFn(adpaterEdgesfn, edgesArr, true)
	smfyNodesfn := opt.Prefix + ".nodes.smfy.Arr"
	NodesArrWriter(nodesArr, smfyNodesfn)
	DBGInfofn := opt.Prefix + ".smfy.DBGInfo"
	DBGInfoWriter(DBGInfofn, len(edgesArr), len(nodesArr))
	//EdgesStatWriter(edgesStatfn, len(edgesArr))
}
