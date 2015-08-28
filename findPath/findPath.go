package findPath

import (
	"bufio"
	// "compress/gzip"
	// "container/list"
	// "encoding/binary"
	// "encoding/gob"
	"container/list"
	"fmt"
	"ga/bnt"
	// "ga/constructcf"
	"ga/constructdbg"
	// "ga/cuckoofilter"
	"io"
	"log"
	"os"
	// "runtime"
	// "math"
	"strconv"

	"github.com/awalterschulze/gographviz"
	"github.com/biogo/hts/bam"
	"github.com/biogo/hts/sam"
	"github.com/jwaldrip/odin/cli"
	"strings"
)

var Kmerlen int
var MAX_READ_LEN int = 450
var MIN_PATH_LEN int = 3

type PathCrossInfo struct {
	EdgeID    constructdbg.DBG_MAX_INT
	Node      constructdbg.DBGNode
	RemainLen int
}

type LA struct {
	RefID   constructdbg.DBG_MAX_INT
	QuyID   constructdbg.DBG_MAX_INT // query ID
	AlgnLen int
	Idty    float64
	RefB    int // the alignment start position of reference
	RefE    int // the alignment end position of reference
	RefLen  int // the reference length
	QuyB    int // the alignment start position of Query
	QuyE    int // the alignment end position of Query
	QuyLen  int // the query length
}

func GetSamRecord(bamfn string, rc chan []sam.Record, numCPU int) {
	fp, err := os.Open(bamfn)
	if err != nil {
		log.Fatalf("[GetSamRecord] open file: %s failed, err: %v\n", bamfn, err)
	}
	defer fp.Close()
	bamfp, err := bam.NewReader(fp, 0)
	if err != nil {
		log.Fatalf("[GetSamRecord] create bam.NewReader err: %v\n", err)
	}
	defer bamfp.Close()
	var rArr []sam.Record
	// var cigar sam.Cigar
	var NM = sam.Tag{'N', 'M'}
	var AS = sam.Tag{'A', 'S'}
	for {
		r, err := bamfp.Read()
		if err != nil {
			break
		}
		if len(r.Cigar) < 2 || len(r.Cigar) > 3 {
			continue
		} else {
			v := r.AuxFields.Get(NM).Value()
			vi := 0
			switch v.(type) {
			case uint8:
				vi = int(v.(uint8))
			case uint16:
				vi = int(v.(uint16))
			}
			if vi != 0 {
				continue
			}
			as := r.AuxFields.Get(AS).Value()
			asi := 0
			switch as.(type) {
			case uint8:
				asi = int(as.(uint8))
			case uint16:
				asi = int(as.(uint16))
			case uint32:
				asi = int(as.(uint32))
			default:
				log.Fatalf("[GetSamRecord] as type unknown\n")
			}
			// fmt.Printf("[GetSamRecord]AS:%d, cigar: %v\n", asi, r.Cigar.String())
			if asi < Kmerlen {
				continue
			}
		}
		// Debug
		// if r.Cigar.String() == "400M" {
		// 	continue
		// }
		//Debug
		if len(rArr) > 0 {
			if rArr[0].Name != r.Name {
				if len(rArr) >= 3 {
					rc <- rArr
					rArr = nil
				} else {
					rArr = rArr[0:0]
				}
			}
		}
		rArr = append(rArr, *r)
	}
	if len(rArr) >= 3 {
		rc <- rArr
	}
	// send terminal signals
	for i := 0; i < numCPU; i++ {
		var nilArr []sam.Record
		rc <- nilArr
	}
}

func AccumulateCigar(cigar sam.Cigar) (Mnum, Inum, Dnum int) {
	for _, co := range cigar {
		if co.Type() == sam.CigarInsertion {
			Inum += co.Len()
		} else if co.Type() == sam.CigarDeletion {
			Dnum += co.Len()
		} else if co.Type() == sam.CigarMatch {
			Mnum += co.Len()
		}
	}

	return
}

func findNextEdge(node constructdbg.DBGNode, eID constructdbg.DBG_MAX_INT, rArr []sam.Record) (neID constructdbg.DBG_MAX_INT, recd sam.Record) {

	var direction uint8
	// find direction
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if node.EdgeIDIncoming[i] == eID {
			direction = constructdbg.FORWARD
			break
		}
		if node.EdgeIDOutcoming[i] == eID {
			direction = constructdbg.BACKWARD
			break
		}
	}
	// get Reference ID arr
	var refIDArr []constructdbg.DBG_MAX_INT
	for _, v := range rArr {
		id, err := strconv.Atoi(v.Ref.Name())
		if err != nil {
			log.Fatalf("[findNextEdge] ref Name not numberical err: %v\n", err)
		}
		refIDArr = append(refIDArr, constructdbg.DBG_MAX_INT(id))
	}

	num := 0
	var teID constructdbg.DBG_MAX_INT
	for i := 0; i < bnt.BaseTypeNum; i++ {
		id := node.EdgeIDIncoming[i]
		if direction == constructdbg.FORWARD {
			id = node.EdgeIDOutcoming[i]
		}
		for j, v := range refIDArr {
			if id == v {
				teID = id
				recd = rArr[j]
				num++
			}
		}
		if num == 1 {
			neID = teID
		}
	}

	return
}

func Cigar2String(cigar sam.Cigar) (cs string) {
	for _, v := range cigar {
		cs += strconv.Itoa(v.Len()) + "---"
	}
	return
}

func isInEdgesArr(arr []constructdbg.DBG_MAX_INT, e constructdbg.DBG_MAX_INT) bool {
	for _, v := range arr {
		if v == e {
			return true
		}
	}
	return false
}

func paraFindShortMappingPath(rc chan []sam.Record, wc chan []constructdbg.DBG_MAX_INT, edgesArr []constructdbg.DBGEdge, nodesArr []constructdbg.DBGNode) {
	// totalM, totalI, totalD := 0, 0, 0
	for {
		rArr := <-rc
		if len(rArr) == 0 {
			var guardPath []constructdbg.DBG_MAX_INT
			wc <- guardPath
			break
		}

		// check rArr[0], cigar type
		r0 := rArr[0]
		clip, match, other := 0, 0, 0
		matchLen := 0
		for i := 0; i < len(r0.Cigar); i++ {
			switch r0.Cigar[i].Type() {
			case sam.CigarSoftClipped, sam.CigarHardClipped:
				clip++
			case sam.CigarMatch:
				match++
				matchLen = r0.Cigar[i].Len()
			default:
				other++
			}
		}
		if len(r0.Cigar) == 2 {
			if clip == 1 && match == 1 && other == 0 && (r0.Start() == 0 || r0.Start() == r0.Len()-matchLen) {

			} else {
				continue
			}
		} else {
			if r0.Cigar[1].Type() == sam.CigarMatch && clip == 2 && other == 0 && r0.Cigar[1].Len() == r0.Ref.Len() {

			} else {
				continue
			}
		}

		var left, right []constructdbg.DBG_MAX_INT
		id, err := strconv.Atoi(r0.Ref.Name())
		if err != nil {
			log.Fatalf("[paraFindShortMappingPath] eid convert err: %v\n", err)
		}
		eID := constructdbg.DBG_MAX_INT(id)
		for i := 0; i < len(r0.Cigar); i++ {
			cop := r0.Cigar[i]
			if cop.Type() == sam.CigarMatch {
				continue
			}
			var nID constructdbg.DBG_MAX_INT
			if i < 1 {
				nID = edgesArr[eID].StartNID
			} else {
				nID = edgesArr[eID].EndNID
			}
			teID := eID
			for {
				neID, recd := findNextEdge(nodesArr[nID], teID, rArr[1:])
				// fmt.Printf("[paraFindShortMappingPath] neID: %v, recd:%v\nedge:%v\n", neID, recd.Cigar.String(), edgesArr[teID])
				if neID == 0 {
					break
				}
				if neID != eID && isInEdgesArr(left, neID) == false && isInEdgesArr(right, neID) == false {
					if i < 1 {
						left = append(left, neID)
					} else {
						right = append(right, neID)
					}
				}
				if len(recd.Cigar) == 3 {
					if recd.Cigar[1].Type() == sam.CigarMatch && recd.Cigar[1].Len() == recd.Ref.Len() {
						id, err = strconv.Atoi(recd.Ref.Name())
						teID = constructdbg.DBG_MAX_INT(id)
						if edgesArr[teID].StartNID == nID {
							nID = edgesArr[teID].EndNID
						} else {
							nID = edgesArr[teID].StartNID
						}
						if nID == 0 {
							break
						}
					}
				} else {
					break
				}
			}
		}
		// cat left and right edges Array
		var catArr []constructdbg.DBG_MAX_INT
		for i := len(left) - 1; i >= 0; i-- {
			catArr = append(catArr, left[i])
		}
		catArr = append(catArr, eID)
		catArr = append(catArr, right...)
		if len(catArr) >= 3 {
			wc <- catArr
		}

		// if len(rArr) > 5 {
		// 	for i := 0; i < len(rArr); i++ {
		// 		fmt.Printf("[paraFindShortMappingPath]refName: %v, refLen: %v, pos:%v, cigar: %v\n", rArr[i].Ref.Name(), rArr[i].Ref.Len(), rArr[i].Start(), rArr[i].Cigar.String())
		// 	}
		// 	fmt.Printf("[paraFindShortMappingPath]\n")
		// }

		// for _, v := range rArr[:1] {
		// 	var NM, MD, SA sam.Tag
		// 	NM[0] = 'N'
		// 	NM[1] = 'M'
		// 	MD[0] = 'M'
		// 	MD[1] = 'D'
		// 	SA[0] = 'S'
		// 	SA[1] = 'A'
		// 	nm := v.AuxFields.Get(NM).Value()
		// 	var nmv int
		// 	switch nm.(type) {
		// 	case uint8:
		// 		nmv = int(nm.(uint8))
		// 	case uint16:
		// 		nmv = int(nm.(uint16))
		// 	}
		// 	// nmv, err := strconv.Atoi(nm.String()[5:])
		// 	// if err != nil {
		// 	// 	log.Fatalf("[paraFindPacbioMappingPath] convert 'NM' err: %v\n", nmv)
		// 	// }
		// 	// fmt.Printf("[paraFindPacbioMappingPath]nm type: %v\n", v.AuxFields.Get(NM).Type())
		// 	// if nm != nil {
		// 	// 	// snm = nm.Value()
		// 	// } else {
		// 	// 	log.Fatalf("[paraFindPacbioMappingPath] nm: %v\n", v.Ref)
		// 	// }
		// 	Mnum, Inum, Dnum := AccumulateCigar(v.Cigar)
		// 	if Mnum < 1000 {
		// 		continue
		// 	}
		// 	fmt.Printf("ref\tpos\tmapQ\tNM\tCigar\n")
		// 	totalM += Mnum
		// 	totalI += Inum
		// 	totalD += Dnum
		// 	totalMis += (int(nmv) - Inum - Dnum)
		// 	// as := Cigar2String(acgr)
		// 	fmt.Printf("%s\t%d\t%v\t%v\n", v.Ref.Name(), v.Pos, v.MapQ, v.Cigar)
		// 	// sav := v.AuxFields.Get(SA)
		// 	// // fmt.Printf("%v\n", sav)
		// 	// if sav != nil {
		// 	// 	sa := v.AuxFields.Get(SA).Value().(string)
		// 	// 	// fmt.Printf("%v\n", sa)
		// 	// 	// sav := strings.Split(sa, ":")
		// 	// 	// fmt.Printf("%v\n", sav)
		// 	// 	saArr := strings.Split(sa[:len(sa)-1], ";")
		// 	// 	for _, e := range saArr {
		// 	// 		arr := strings.Split(e, ",")
		// 	// 		// fmt.Printf("%v\n", arr)
		// 	// 		// cgr, _ := sam.ParseCigar([]byte(arr[3]))
		// 	// 		// cr := AccumulateCigar(cgr)
		// 	// 		// as := Cigar2String(cr)
		// 	// 		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", arr[0], arr[1], arr[4], arr[5], arr[3])
		// 	// 	}

		// 	// }
		// }
	}

	// fmt.Printf("[paraFindPacbioMappingPath] totalM: %d, totalMis: %d, totalI: %d, totalD: %d\n", totalM, totalMis, totalI, totalD)
}

func IsCyclePath(path []constructdbg.DBG_MAX_INT) bool {
	cycle := false
	for i := 0; i < len(path); i++ {
		for j := 0; j < i; j++ {
			if path[i] == path[j] {
				cycle = true
				break
			}
		}
	}
	return cycle
}

func IsNodeCyclePath(path []constructdbg.DBG_MAX_INT, edgesArr []constructdbg.DBGEdge) bool {
	// node array
	var narr []constructdbg.DBG_MAX_INT
	for i := 1; i < len(path); i++ {
		e1 := edgesArr[path[i-1]]
		e2 := edgesArr[path[i]]
		if e1.StartNID == e2.StartNID || e1.StartNID == e2.EndNID {
			if e1.EndNID == e2.StartNID || e1.EndNID == e2.EndNID {
				return true
			} else {
				narr = append(narr, e1.StartNID)
			}
		} else if e1.EndNID == e2.StartNID || e1.EndNID == e2.EndNID {
			narr = append(narr, e1.EndNID)
		} else {
			log.Fatalf("[IsNodeCyclePath] not found Identity Node e1: %v\ne2: %v\n", e1, e2)
		}
	}
	var start, end constructdbg.DBG_MAX_INT
	// fmt.Printf("[IsNodeCyclePath] path: %v, narr: %v\n", path, narr)
	if edgesArr[path[0]].StartNID == narr[0] {
		start = edgesArr[path[0]].EndNID
	} else {
		start = edgesArr[path[0]].StartNID
	}
	if edgesArr[path[len(path)-1]].StartNID == narr[len(narr)-1] {
		end = edgesArr[path[len(path)-1]].EndNID
	} else {
		end = edgesArr[path[len(path)-1]].StartNID
	}
	if start > 0 && start == end {
		return true
	}
	if start > 0 {
		var tmp []constructdbg.DBG_MAX_INT
		tmp = append(tmp, start)
		narr = append(tmp, narr...)
	}
	if end > 0 {
		narr = append(narr, end)
	}

	// check cycle
	return IsCyclePath(narr)

}

func WriteShortPathToDBG(wc chan []constructdbg.DBG_MAX_INT, edgesArr []constructdbg.DBGEdge, numCPU int) {
	terminalNum := 0
	for {
		path := <-wc
		if len(path) == 0 {
			terminalNum++
			if terminalNum == numCPU {
				break
			} else {
				continue
			}

		}

		// write path to the DBG edgesArr
		{
			if IsCyclePath(path) {
				continue
			}
			edgesArr[path[0]].InsertPathToEdge(path, 1)
			path = constructdbg.ReverseDBG_MAX_INTArr(path)
			edgesArr[path[0]].InsertPathToEdge(path, 1)
		}
		if len(path) > 5 {
			fmt.Printf("[WriteShortPathToDBG] pathArr: %v\n", path)
		}
	}
}

func GetNextEID(eID constructdbg.DBG_MAX_INT, node constructdbg.DBGNode) (neID constructdbg.DBG_MAX_INT) {
	var direction uint8
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if node.EdgeIDIncoming[i] == eID {
			direction = constructdbg.BACKWARD
			break
		}
		if node.EdgeIDOutcoming[i] == eID {
			direction = constructdbg.FORWARD
			break
		}
	}
	if direction != constructdbg.BACKWARD && direction != constructdbg.FORWARD {
		log.Fatalf("[GetNextEID] direction not set\n")
	}
	num := 0
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if direction == constructdbg.FORWARD {
			if node.EdgeIDIncoming[i] > 0 {
				num++
				neID = node.EdgeIDIncoming[i]
			}
		} else {
			if node.EdgeIDOutcoming[i] > 0 {
				num++
				neID = node.EdgeIDOutcoming[i]
			}
		}
	}
	if num != 1 {
		log.Fatalf("[GetNextEID] found %d edges\n", num)
	}
	return neID
}

func IsDirectionUniqueEdge(edge constructdbg.DBGEdge, node constructdbg.DBGNode) bool {
	var direction uint8
	in, out := 0, 0
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if node.EdgeIDIncoming[i] > 0 {
			in++
			if node.EdgeIDIncoming[i] == edge.ID {
				direction = constructdbg.BACKWARD
			}
		}
		if node.EdgeIDOutcoming[i] > 0 {
			out++
			if node.EdgeIDOutcoming[i] == edge.ID {
				direction = constructdbg.FORWARD
			}
		}
	}
	if direction == constructdbg.FORWARD {
		if in == 1 {
			return true
		} else {
			return false
		}
	} else if direction == constructdbg.BACKWARD {
		if out == 1 {
			return true
		} else {
			return false
		}
	} else {
		return false
		// log.Fatalf("[IsDirectionUniqueEdge] direction set error\nnode:%v\nedge:%v\n", node, edge)
	}

	return false
}

func IsUniqueEdge(edge constructdbg.DBGEdge, nodesArr []constructdbg.DBGNode) bool {
	if IsDirectionUniqueEdge(edge, nodesArr[edge.StartNID]) == false {
		return false
	}
	if IsDirectionUniqueEdge(edge, nodesArr[edge.EndNID]) == false {
		return false
	}
	return true
}

func MergePath(pathMat []constructdbg.Path, beID constructdbg.DBG_MAX_INT) (uniquePath constructdbg.Path, num int) {
	for _, p := range pathMat {
		if p.IDArr[1] == beID {
			if len(uniquePath.IDArr) == 0 {
				uniquePath = p
				num++
			} else {
				for j, eID := range p.IDArr {
					if j < len(uniquePath.IDArr) {
						if eID != uniquePath.IDArr[j] {
							num++
							break
						}
					} else {
						break
					}
				}
				if num == 1 {
					if len(uniquePath.IDArr) < len(p.IDArr) {
						uniquePath = p
					}
				} else {
					break
				}
			}
		}
	}
	return
}

func IndexEID(arr []constructdbg.DBG_MAX_INT, eID constructdbg.DBG_MAX_INT) int {
	for i, id := range arr {
		if id == eID {
			return i
		}
	}
	return -1
}

func IndexUniqueEdge(consisPathArr []constructdbg.DBG_MAX_INT, edgesArr []constructdbg.DBGEdge, direction uint8) int {
	if direction == constructdbg.FORWARD {
		for i, eID := range consisPathArr {
			if edgesArr[eID].GetUniqueFlag() > 0 {
				return i
			}
		}
	} else if direction == constructdbg.BACKWARD {
		for i := len(consisPathArr) - 1; i >= 0; i-- {
			if edgesArr[consisPathArr[i]].GetUniqueFlag() > 0 {
				return i
			}
		}
	} else {
		log.Fatalf("[IndexUniqueEdge] direction set error\n")
	}

	return -1
}

func GetReverseDBG_MAX_INTArr(arr []constructdbg.DBG_MAX_INT) []constructdbg.DBG_MAX_INT {
	la := len(arr)
	rarr := make([]constructdbg.DBG_MAX_INT, la)
	for i := la - 1; i >= 0; i-- {
		rarr[la-i-1] = arr[i]
	}

	return rarr

}

func findConsistenceAndMergePath(consisPathArr1, consisPathArr2 []constructdbg.DBG_MAX_INT, eID1, eID2 constructdbg.DBG_MAX_INT) (mergePathArr []constructdbg.DBG_MAX_INT) {
	idx1 := IndexEID(consisPathArr1, eID1)
	idx2 := IndexEID(consisPathArr1, eID2)

	idx3 := IndexEID(consisPathArr2, eID1)
	idx4 := IndexEID(consisPathArr2, eID2)
	if idx1 < 0 || idx2 < 0 || idx3 < 0 || idx4 < 0 {
		log.Fatalf("[findConsistenceAndMergePath] eID not found in the consisPathArr\n")
	}

	if idx1 > idx2 {
		if idx3 < idx4 {
			log.Fatalf("[findConsistenceAndMergePath] two consisPathArr not consistence arr1: %v, arr2:%v\n", consisPathArr1, consisPathArr2)
		}
		idx1, idx2 = idx2, idx1
		idx3, idx4 = idx4, idx3
	} else {
		if idx3 > idx4 {
			log.Fatalf("[findConsistenceAndMergePath] two consisPathArr not consistence arr1: %v, arr2:%v\n", consisPathArr1, consisPathArr2)
		}
	}

	var consisFlag bool = true
	if idx1 <= idx3 {
		i := 0
		j := idx3 - idx1
		for i < len(consisPathArr1) && j < len(consisPathArr2) {
			if consisPathArr1[i] != consisPathArr2[j] {
				consisFlag = false
				break
			}
			i++
			j++
		}
		if consisFlag == false {
			return mergePathArr
		} else {
			if i < len(consisPathArr1) {
				mergePathArr = append(consisPathArr2, consisPathArr1[i:]...)
			} else {
				mergePathArr = consisPathArr2
			}
		}
	} else {
		i := idx1 - idx3
		j := 0
		for i < len(consisPathArr1) && j < len(consisPathArr2) {
			if consisPathArr1[i] != consisPathArr2[j] {
				consisFlag = false
				break
			}
			i++
			j++
		}
		if consisFlag == false {
			return mergePathArr
		} else {
			if j < len(consisPathArr2) {
				mergePathArr = append(consisPathArr1, consisPathArr2[j:]...)
			} else {
				mergePathArr = consisPathArr1
			}
		}
	}
	return mergePathArr
}

func SubstituteEdgeID(nodesArr []constructdbg.DBGNode, nID, srcEID, dstEID constructdbg.DBG_MAX_INT) bool {
	var success bool
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if nodesArr[nID].EdgeIDIncoming[i] == srcEID {
			nodesArr[nID].EdgeIDIncoming[i] = dstEID
			success = true
			break
		}
		if nodesArr[nID].EdgeIDOutcoming[i] == srcEID {
			nodesArr[nID].EdgeIDOutcoming[i] = dstEID
			success = true
			break
		}
	}
	return success
}

func GetDirection(node constructdbg.DBGNode, eID constructdbg.DBG_MAX_INT) (direction uint8) {
	if eID == 0 {
		log.Fatalf("[GetDirection] not allow found eid:%d\n", eID)
	}
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if eID == node.EdgeIDIncoming[i] {
			direction = constructdbg.BACKWARD
			break
		} else if eID == node.EdgeIDOutcoming[i] {
			direction = constructdbg.FORWARD
			break
		}
	}
	if direction != constructdbg.BACKWARD && direction != constructdbg.FORWARD {
		log.Fatalf("[GetDirection] direction error\n")
	}

	return direction
}

func connectNodeID(e1, e2 constructdbg.DBGEdge) constructdbg.DBG_MAX_INT {
	if e1.StartNID == e2.StartNID || e1.StartNID == e2.EndNID {
		return e1.StartNID
	} else if e1.EndNID == e2.StartNID || e1.EndNID == e2.EndNID {
		return e1.EndNID
	} else {
		log.Fatalf("[connectNodeID] Not found connect node ID\n")
	}
	return 0
}

func ResetProcessFlag(edgesArr []constructdbg.DBGEdge) {
	for i, _ := range edgesArr {
		edgesArr[i].ResetProcessFlag()
	}
}

func DeleteEdgeID(nodesArr []constructdbg.DBGNode, nID, eID constructdbg.DBG_MAX_INT) (success bool) {
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if nodesArr[nID].EdgeIDIncoming[i] == eID {
			nodesArr[nID].EdgeIDIncoming[i] = 0
			success = true
			break
		}
		if nodesArr[nID].EdgeIDOutcoming[i] == eID {
			nodesArr[nID].EdgeIDOutcoming[i] = 0
			success = true
			break
		}
	}

	return success
}

func FindMaxUnqiuePath(edgesArr []constructdbg.DBGEdge, nodesArr []constructdbg.DBGNode) {

	// merge all cross edge paths to the PathMat
	for _, e := range edgesArr {
		if e.ID == 0 || len(e.Utg.Ks) >= MAX_READ_LEN {
			continue
		}
		if IsUniqueEdge(e, nodesArr) == false {
			continue
		}
		var eID constructdbg.DBG_MAX_INT
		var node constructdbg.DBGNode
		leID := GetNextEID(e.ID, nodesArr[e.StartNID])
		_, lnum := MergePath(e.PathMat, leID)
		reID := GetNextEID(e.ID, nodesArr[e.EndNID])
		_, rnum := MergePath(e.PathMat, reID)
		if lnum != 1 || rnum != 1 {
			continue
		}
		eID = reID
		node = nodesArr[e.EndNID]

		remainLen := MAX_READ_LEN - len(e.Utg.Ks)
		stk := list.New()
		var pci PathCrossInfo
		pci.EdgeID, pci.Node, pci.RemainLen = eID, node, remainLen
		stk.PushBack(pci)
		for stk.Len() > 0 {
			ele := stk.Back()
			stk.Remove(ele)
			pci = ele.Value.(PathCrossInfo)
			if pci.RemainLen <= 0 {
				log.Fatalf("[FindMaxUnqiuePath] RemainLen smaller than zero\n")
			}
			ne := edgesArr[pci.EdgeID]
			for _, path := range ne.PathMat {
				j := IndexEID(path.IDArr, e.ID)
				if MIN_PATH_LEN-1 <= j && j < len(path.IDArr)-1 {
					slc := GetReverseDBG_MAX_INTArr(path.IDArr[:j+1])
					edgesArr[e.ID].InsertPathToEdge(slc, path.Freq)
					if len(path.IDArr)-j >= MIN_PATH_LEN {
						edgesArr[e.ID].InsertPathToEdge(path.IDArr[j:], path.Freq)
					}
				}
			}

			pci.RemainLen -= (len(ne.Utg.Ks) - Kmerlen + 1)
			if pci.RemainLen <= 0 {
				continue
			}

			// add to the stack next edges
			if ne.StartNID == pci.Node.ID {
				node = nodesArr[ne.EndNID]
			} else {
				node = nodesArr[ne.StartNID]
			}
			direction := GetDirection(node, pci.EdgeID)
			for j := 0; j < bnt.BaseTypeNum; j++ {
				if direction == constructdbg.FORWARD && node.EdgeIDIncoming[j] > 0 {
					var npci PathCrossInfo
					npci.EdgeID = node.EdgeIDIncoming[j]
					npci.Node = node
					npci.RemainLen = pci.RemainLen
					stk.PushBack(npci)
				} else if direction == constructdbg.BACKWARD && node.EdgeIDOutcoming[j] > 0 {
					var npci PathCrossInfo
					npci.EdgeID = node.EdgeIDOutcoming[j]
					npci.Node = node
					npci.RemainLen = pci.RemainLen
					stk.PushBack(npci)
				}
			}
		}
	}

	// fmt.Printf("[FindMaxUnqiuePath]<DeBug>edgesArr[382].PathMat: %v\n", edgesArr[382].PathMat)
	// find edge max unique path
	consistenceA := make([][]constructdbg.DBG_MAX_INT, len(edgesArr))
	{
		for _, e := range edgesArr {
			if e.ID == 0 {
				continue
			}
			if IsUniqueEdge(e, nodesArr) == false {
				continue
			}

			beID := GetNextEID(e.ID, nodesArr[e.StartNID])
			buniquePath, bnum := MergePath(e.PathMat, beID)
			feID := GetNextEID(e.ID, nodesArr[e.EndNID])
			funiquePath, fnum := MergePath(e.PathMat, feID)
			if bnum > 1 || fnum > 1 {
				continue
			}

			if bnum == 1 {
				tp := GetReverseDBG_MAX_INTArr(buniquePath.IDArr)
				consistenceA[e.ID] = tp
				edgesArr[e.ID].SetUniqueFlag()
			}
			if fnum == 1 {
				if len(consistenceA[e.ID]) > 0 {
					consistenceA[e.ID] = append(consistenceA[e.ID], funiquePath.IDArr[1:]...)
					if IsCyclePath(consistenceA[e.ID]) {
						consistenceA[e.ID] = nil
					}

				} else {
					consistenceA[e.ID] = funiquePath.IDArr
					edgesArr[e.ID].SetUniqueFlag()
				}
			}
		}
	}
	// fmt.Printf("[FindMaxUnqiuePath]edgesArr[353].PathMat: %v\n", consistenceA[353])

	// extend edges max unique path
	for i, e := range edgesArr {
		if e.GetProcessFlag() > 0 {
			continue
		}
		edgesArr[i].SetProcessFlag()

		if len(consistenceA[i]) < MIN_PATH_LEN {
			continue
		}
		// Debug
		if len(consistenceA[i]) >= 5 {
			fmt.Printf("[FindMaxUnqiuePath] consisteance[%d] Path: %v\n", i, consistenceA[i])
		}
		j := IndexEID(consistenceA[i], e.ID)
		if j < 0 {
			log.Fatalf("[FindMaxUnqiuePath] not index Edge ID: %d\n", i)
		}
		// extend left region
		if j >= MIN_PATH_LEN-1 {
			extendPathLen := j
			se := e
			for x := extendPathLen - 1; x >= 0; x-- {
				ne := edgesArr[consistenceA[i][x]]
				if len(consistenceA[ne.ID]) < MIN_PATH_LEN || IsUniqueEdge(ne, nodesArr) == false {
					continue
				}
				y := IndexEID(consistenceA[ne.ID], ne.ID)
				z := IndexEID(consistenceA[ne.ID], se.ID)
				if y >= 0 && z >= 0 {
					if y >= z {
						consistenceA[ne.ID] = GetReverseDBG_MAX_INTArr(consistenceA[ne.ID])
					}
					consistenceMergePath := findConsistenceAndMergePath(consistenceA[i], consistenceA[ne.ID], se.ID, ne.ID)
					if len(consistenceMergePath) >= len(consistenceA[i]) {
						if IsCyclePath(consistenceMergePath) {
							break
						}
						consistenceA[i] = consistenceMergePath
						x = IndexEID(consistenceA[i], ne.ID)
						edgesArr[ne.ID].SetProcessFlag()
						consistenceA[ne.ID] = nil
						se = ne
					} else {
						consistenceA[ne.ID] = nil
					}
				}
			}
		}
		if len(consistenceA[i])-j >= MIN_PATH_LEN { // extend right region
			se := e
			for x := j + 1; x < len(consistenceA[i]); x++ {
				ne := edgesArr[consistenceA[i][x]]
				if len(consistenceA[ne.ID]) < MIN_PATH_LEN || IsUniqueEdge(ne, nodesArr) {
					continue
				}
				y := IndexEID(consistenceA[ne.ID], ne.ID)
				z := IndexEID(consistenceA[ne.ID], se.ID)
				if y >= 0 && z >= 0 {
					if z >= y {
						consistenceA[ne.ID] = GetReverseDBG_MAX_INTArr(consistenceA[ne.ID])
					}
					consistenceMergePath := findConsistenceAndMergePath(consistenceA[i], consistenceA[ne.ID], se.ID, ne.ID)
					if len(consistenceMergePath) >= len(consistenceA[i]) {
						if IsCyclePath(consistenceMergePath) {
							break
						}
						consistenceA[i] = consistenceMergePath
						x = IndexEID(consistenceA[i], ne.ID)
						edgesArr[ne.ID].SetProcessFlag()
						consistenceA[ne.ID] = nil
						se = ne
					} else {
						consistenceA[ne.ID] = nil
					}
				}
			}
		}
	}

	// merge unique path
	ResetProcessFlag(edgesArr)
	{
		for i, _ := range edgesArr {
			if edgesArr[i].GetProcessFlag() > 0 || edgesArr[i].GetDeleteFlag() > 0 || len(consistenceA[i]) < MIN_PATH_LEN {
				continue
			}
			x := IndexUniqueEdge(consistenceA[i], edgesArr, constructdbg.FORWARD)
			y := IndexUniqueEdge(consistenceA[i], edgesArr, constructdbg.BACKWARD)
			if x < y && y-x+1 >= MIN_PATH_LEN {
				// tmp1 := consistenceA[i][0]
				// tmp2 := consistenceA[i][len(consistenceA[i])-1]
				// fmt.Printf("[FindMaxUnqiuePath]i: %d, merge path: %v, x:%d, y:%d\npathMat[%d]: %v\npathMat[%d]: %v\n", i, consistenceA[i], x, y, tmp1, edgesArr[tmp1].PathMat, tmp2, edgesArr[tmp2].PathMat)
				// e2 := edgesArr[consistenceA[i][x+1]]
				// if e1.StartNID == e2.StartNID || e1.StartNID == e2.EndNID {
				// 	constructdbg.RCEdge(edgesArr, e1.ID)
				// }
				if IsNodeCyclePath(consistenceA[i][x:y+1], edgesArr) {
					continue
				}
				e1 := edgesArr[consistenceA[i][x]]
				fmt.Printf("[FindMaxUnqiuePath] MergePath: %v\n", consistenceA[i][x:y+1])
				edgesArr[e1.ID].SetProcessFlag()
				for j := x + 1; j <= y; j++ {
					e2 := edgesArr[consistenceA[i][j]]
					edgesArr[e2.ID].SetProcessFlag()
					nID := connectNodeID(e1, e2)
					fmt.Printf("[FindMaxUnqiuePath] node: %v\ne1:%v\ne2:%v\n", nodesArr[nID], e1, e2)
					if GetDirection(nodesArr[nID], e2.ID) == constructdbg.FORWARD {
						if e1.StartNID == nID {
							constructdbg.RCEdge(edgesArr, e1.ID)
						}
						if e2.EndNID == nID {
							constructdbg.RCEdge(edgesArr, e2.ID)
						}
						e1 = edgesArr[e1.ID]
						e2 = edgesArr[e2.ID]
						DeleteEdgeID(nodesArr, e1.EndNID, e1.ID)
						constructdbg.ConcatEdges(edgesArr, e1.ID, e2.ID, e1.ID)
						// if SubstituteEdgeID(nodesArr, e1.EndNID, e1.ID, 0) == false {
						// 	log.Fatalf("[FindMaxUnqiuePath]1 SubstituteEdgeID failed")
						// }
						// edgesArr[e1.ID].EndNID = e2.EndNID
						if e2.GetUniqueFlag() > 0 {
							edgesArr[e2.ID].SetDeleteFlag()
							DeleteEdgeID(nodesArr, e2.StartNID, e2.ID)
							if SubstituteEdgeID(nodesArr, e2.EndNID, e2.ID, e1.ID) == false {
								log.Fatalf("[FindMaxUnqiuePath]1 SubstituteEdgeID failed")
							}
						}
						e1 = edgesArr[e1.ID]
					} else { // == constructdbg.FORWARD
						if e1.EndNID == nID {
							constructdbg.RCEdge(edgesArr, e1.ID)
						}
						if e2.StartNID == nID {
							constructdbg.RCEdge(edgesArr, e2.ID)
						}
						e1 = edgesArr[e1.ID]
						e2 = edgesArr[e2.ID]
						DeleteEdgeID(nodesArr, e1.StartNID, e1.ID)
						constructdbg.ConcatEdges(edgesArr, e2.ID, e1.ID, e1.ID)
						if e2.GetUniqueFlag() > 0 {
							edgesArr[e2.ID].SetDeleteFlag()
							DeleteEdgeID(nodesArr, e2.EndNID, e2.ID)
							if SubstituteEdgeID(nodesArr, e2.StartNID, e2.ID, e1.ID) == false {
								log.Fatalf("[FindMaxUnqiuePath]1 SubstituteEdgeID failed")
							}
						}
						e1 = edgesArr[e1.ID]
					}
				}
			}
		}
	}

	/*for i, e := range edgesArr {
		// edge has been processed and  Unique
		if e.GetProcessFlag() > 0 {
			continue
		}
		if IsUniqueEdge(e, nodesArr) == false {
			continue
		}
		// constructdbg.FORWARD extend
		feID := GetNextEID(e.ID, nodesArr[e.EndNID])
		uniquePath, num := MergePath(e.PathMat, feID)
		if num == 1 && uniquePath.Freq >= MIN_PATH_FREQ {
			for i := 1; i < len(uniquePath.Path); i++ {
				edge := edgesArr[uniquePath.Path[i]]
				// check path consistence
				if IsConsistencePath(uniquePath.Path, i, edge.PathMat) == false {
					uniquePath.Path = uniquePath.Path[:i]
					break
				}
				if IsUniqueEdge(edge, nodesArr) {
					eid := GetNextEID(edge.ID, nodesArr[edge.EndNID])
					if eid == uniquePath.Path[i-1] {
						eid = GetNextEID(edge.ID, nodesArr[edge.StartNID])
					}
					up, n := MergePath(edge.PathMat)
				}
			}
		}
	}*/
}

func IsComingInNode(node constructdbg.DBGNode, eID constructdbg.DBG_MAX_INT) bool {
	for i := 0; i < bnt.BaseTypeNum; i++ {
		if node.EdgeIDIncoming[i] == eID {
			return true
		}
		if node.EdgeIDOutcoming[i] == eID {
			return true
		}
	}
	return false
}

func CleanDBG(edgesArr []constructdbg.DBGEdge, nodesArr []constructdbg.DBGNode) {
	deleteNum := 0
	for i, e := range edgesArr {
		if e.GetDeleteFlag() > 0 {
			edgesArr[i] = edgesArr[0]
			deleteNum++
		} else {
			if e.StartNID > 0 {
				if IsComingInNode(nodesArr[e.StartNID], e.ID) == false {
					log.Fatalf("[CleanDBG] edge ID not include Node coming arr\nnode: %v\nedge: %v\n", nodesArr[e.StartNID], e)
					// fmt.Printf("[CleanDBG] edge ID not include Node coming arr\nnode: %v\nedge: %v\n", nodesArr[e.StartNID], e)
				}
			}
			if e.EndNID > 0 {
				if IsComingInNode(nodesArr[e.EndNID], e.ID) == false {
					log.Fatalf("[CleanDBG] edge ID not include Node coming arr\nnode: %v\nedge: %v\n", nodesArr[e.StartNID], e)
					// fmt.Printf("[CleanDBG] edge ID not include Node coming arr\nnode: %v\nedge: %v\n", nodesArr[e.EndNID], e)
				}
			}
			edgesArr[i].Flag = 0
		}
	}

	// check and clean node
	for i, n := range nodesArr {
		if n.ID > 0 {
			for j := 0; j < bnt.BaseTypeNum; j++ {
				if n.EdgeIDIncoming[j] > 0 {
					eID := n.EdgeIDIncoming[j]
					if edgesArr[eID].StartNID != n.ID && edgesArr[eID].EndNID != n.ID {
						nodesArr[i].EdgeIDIncoming[j] = 0
					}
				}
				if n.EdgeIDOutcoming[j] > 0 {
					eID := n.EdgeIDOutcoming[j]
					if edgesArr[eID].StartNID != n.ID && edgesArr[eID].EndNID != n.ID {
						nodesArr[i].EdgeIDOutcoming[j] = 0
					}
				}
			}
		}
	}

	fmt.Printf("[SmfyDBG] delete edges number is : %d\n", deleteNum)
}

func GraphvizDBG(nodesArr []constructdbg.DBGNode, edgesArr []constructdbg.DBGEdge, graphfn string) {
	// create a new graph
	g := gographviz.NewGraph()
	g.SetName("G")
	g.SetDir(true)
	g.SetStrict(false)
	for _, v := range nodesArr {
		attr := gographviz.NewAttrs()
		attr.Add("color", "Green")
		attr.Add("shape", "record")
		var labels string
		labels = "{" + strconv.Itoa(int(v.EdgeIDIncoming[0])) + "|" + strconv.Itoa(int(v.EdgeIDIncoming[1])) + "|" + strconv.Itoa(int(v.EdgeIDIncoming[2])) + "|" + strconv.Itoa(int(v.EdgeIDIncoming[3])) + "}|" + strconv.Itoa(int(v.ID)) + "|{" + strconv.Itoa(int(v.EdgeIDOutcoming[0])) + "|" + strconv.Itoa(int(v.EdgeIDOutcoming[1])) + "|" + strconv.Itoa(int(v.EdgeIDOutcoming[2])) + "|" + strconv.Itoa(int(v.EdgeIDOutcoming[3])) + "}"
		attr.Add("label", labels)
		g.AddNode("G", strconv.Itoa(int(v.ID)), attr)
	}
	g.AddNode("G", "0", nil)

	for i := 1; i < len(edgesArr); i++ {
		e := edgesArr[i]
		if e.ID == 0 || e.GetDeleteFlag() > 0 {
			continue
		}
		attr := gographviz.NewAttrs()
		attr.Add("color", "Blue")
		labels := "ID:" + strconv.Itoa(int(e.ID)) + " len:" + strconv.Itoa(len(e.Utg.Ks))
		attr.Add("label", labels)
		g.AddEdge(strconv.Itoa(int(e.StartNID)), strconv.Itoa(int(e.EndNID)), true, attr)
	}
	// output := graph.String()
	gfp, err := os.Create(graphfn)
	if err != nil {
		log.Fatalf("[GraphvizDBG] Create file: %s failed, err: %v\n", graphfn, err)
	}
	defer gfp.Close()
	gfp.WriteString(g.String())
}

func FSpath(c cli.Command) {
	k := c.Parent().Flag("K").String()
	var err error = nil
	Kmerlen, err = strconv.Atoi(k)
	constructdbg.Kmerlen = Kmerlen
	if err != nil {
		log.Fatalf("[Fpath] argument: %s set error: %v\n", k, err)
	}
	numCPU, err := strconv.Atoi(c.Parent().Flag("t").String())
	if err != nil {
		log.Fatalf("[Fpath] argument: 't' set error: %v\n", err)
	}
	prefix := c.Parent().Flag("p").String()
	// read nodes file and transform to array mode, for more quickly access
	smfyNodesfn := prefix + ".nodes.smfy.mmap"
	nodeMap := constructdbg.NodeMapMmapReader(smfyNodesfn)
	nodesStatfn := prefix + ".nodes.stat"
	nodesSize := constructdbg.NodesStatReader(nodesStatfn)
	nodesArr := make([]constructdbg.DBGNode, nodesSize)
	constructdbg.NodeMap2NodeArr(nodeMap, nodesArr)
	nodeMap = nil
	// Restore edges info
	edgesStatfn := prefix + ".edges.stat"
	edgesSize := constructdbg.EdgesStatReader(edgesStatfn)
	edgesArr := make([]constructdbg.DBGEdge, edgesSize)
	edgesfn := prefix + ".edges.smfy.fq"
	constructdbg.LoadEdgesfqFromFn(edgesfn, edgesArr)

	bamfn := prefix + ".bam"
	rc := make(chan []sam.Record, numCPU*2)
	wc := make(chan []constructdbg.DBG_MAX_INT, numCPU*2)
	// defer rc.Close()
	go GetSamRecord(bamfn, rc, numCPU)

	for i := 0; i < numCPU; i++ {
		go paraFindShortMappingPath(rc, wc, edgesArr, nodesArr)
	}

	WriteShortPathToDBG(wc, edgesArr, numCPU)

	// Find Max unique path   and merge neighbour edges
	// fmt.Printf("[FSpath] edgesArr[76]: %v\n", edgesArr[76])
	FindMaxUnqiuePath(edgesArr, nodesArr)
	CleanDBG(edgesArr, nodesArr)
	// simplify DBG
	// SmfyDBG(edgesArr, nodesArr)
	graphfn := prefix + ".ShortPath.dot"
	GraphvizDBG(nodesArr, edgesArr, graphfn)
	// Write to files
	edgesfn = prefix + ".edges.ShortPath.fq"
	constructdbg.StoreEdgesToFn(edgesfn, edgesArr, true)
	// constructdbg.StoreEdgesToFn(edgesfn, edgesArr, false)
	nodesfn := prefix + ".nodes.ShortPath.Arr"
	constructdbg.NodesArrWriter(nodesArr, nodesfn)
}

func Convert2LA(fields []string) (la LA) {
	id, err := strconv.Atoi(fields[0])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[0])
	}
	la.RefID = constructdbg.DBG_MAX_INT(id)
	id, err = strconv.Atoi(fields[1])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[1])
	}
	la.QuyID = constructdbg.DBG_MAX_INT(id)
	la.AlgnLen, err = strconv.Atoi(fields[2])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[2])
	}
	if la.AlgnLen < 0 {
		la.AlgnLen = 0 - la.AlgnLen
	}
	la.Idty, err = strconv.ParseFloat(fields[3], 64)
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[3])
	}
	la.RefB, err = strconv.Atoi(fields[5])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[5])
	}
	la.RefE, err = strconv.Atoi(fields[6])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[6])
	}
	la.RefLen, err = strconv.Atoi(fields[7])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[7])
	}
	la.QuyB, err = strconv.Atoi(fields[9])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[9])
	}
	la.QuyE, err = strconv.Atoi(fields[10])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[10])
	}
	la.QuyLen, err = strconv.Atoi(fields[11])
	if err != nil {
		log.Fatalf("[Convert2LA] convert %s error\n", fields[11])
	}

	return la
}

func InsertIDArr(RefIDArr []constructdbg.DBG_MAX_INT, ID constructdbg.DBG_MAX_INT) []constructdbg.DBG_MAX_INT {
	for _, e := range RefIDArr {
		if ID == e {
			return RefIDArr
		}
	}
	RefIDArr = append(RefIDArr, ID)
	return RefIDArr
}

func GetLARecord(lafn string, lac chan []LA, numCPU int) {
	fp, err := os.Open(lafn)
	if err != nil {
		log.Fatalf("[GetSamRecord] open file: %s failed, err: %v\n", lafn, err)
	}
	defer fp.Close()
	lafp := bufio.NewReader(fp)
	var laArr []LA
	var RefIDArr []constructdbg.DBG_MAX_INT
	eof := false
	for !eof {
		line, err := lafp.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				err = nil
				eof = true
			} else {
				log.Fatalf("[GetLARecord] read %s file error\n", lafn)
			}
		}
		fields := strings.Fields(line)
		la := Convert2LA(fields)
		if len(laArr) > 0 {
			if laArr[0].QuyID != la.QuyID {
				if len(RefIDArr) >= MIN_PATH_LEN {
					lac <- laArr
				}
				laArr = nil
				RefIDArr = RefIDArr[:0]
			}
		}
		laArr = append(laArr, la)
		RefIDArr = InsertIDArr(RefIDArr, la.RefID)

	}
	if len(laArr) >= MIN_PATH_LEN && len(RefIDArr) >= MIN_PATH_LEN {
		lac <- laArr
	}
	// send terminal signals
	for i := 0; i < numCPU; i++ {
		var nilArr []LA
		lac <- nilArr
	}
}

func paraFindLongMappingPath(lac chan []LA, wc chan []constructdbg.DBG_MAX_INT, edgesArr []constructdbg.DBGEdge, nodesArr []constructdbg.DBGNode, RefIDMapArr []constructdbg.DBG_MAX_INT) {
	for {
		laArr := <-lac
		if len(laArr) == 0 {
			var guardPath []constructdbg.DBG_MAX_INT
			wc <- guardPath
			break
		}

		var path []constructdbg.DBG_MAX_INT
		index, begin, end, RefLen := GetMaxMappingLength(laArr)
		eID := RefIDMapArr[index]
		edge := edgesArr[eID]
		fmt.Printf("[paraFindLongMappingPath] RefLen: %d, edges Length: %d\n", RefLen, len(edge.Utg.Ks))
		if begin < Min()


	}
}

func FLpath(c cli.Command) {
	k := c.Parent().Flag("K").String()
	var err error = nil
	Kmerlen, err = strconv.Atoi(k)
	constructdbg.Kmerlen = Kmerlen
	if err != nil {
		log.Fatalf("[Fpath] argument: %s set error: %v\n", k, err)
	}
	numCPU, err := strconv.Atoi(c.Parent().Flag("t").String())
	if err != nil {
		log.Fatalf("[Fpath] argument: 't' set error: %v\n", err)
	}
	prefix := c.Parent().Flag("p").String()
	// read nodesArr file
	spNodesfn := prefix + ".nodes.ShortPath.Arr"
	nodesArr := constructdbg.NodesArrReader(spNodesfn)
	// Restore edges info
	edgesStatfn := prefix + ".edges.stat"
	edgesSize := constructdbg.EdgesStatReader(edgesStatfn)
	edgesArr := make([]constructdbg.DBGEdge, edgesSize)
	edgesfn := prefix + ".edges.ShortPath.fq"
	constructdbg.LoadEdgesfqFromFn(edgesfn, edgesArr)

	LongReadPathfn := prefix + ".LA"
	lac := make(chan []LA, numCPU*2)
	wc := make(chan []constructdbg.DBG_MAX_INT, numCPU*2)
	RefIDMapArr := GetOrderID(edgesArr)
	// defer rc.Close()
	go GetLARecord(LongReadPathfn, lac, numCPU)

	for i := 0; i < numCPU; i++ {
		go paraFindLongMappingPath(lac, wc, edgesArr, nodesArr, RefIDMapArr)
	}

	WriteLongPathToDBG(wc, edgesArr, numCPU)

	// Find Max unique path   and merge neighbour edges
	// fmt.Printf("[FSpath] edgesArr[76]: %v\n", edgesArr[76])
	FindMaxUnqiuePath(edgesArr, nodesArr)
	// simplify DBG
	// SmfyDBG(edgesArr, nodesArr)
	graphfn := prefix + ".LongPath.dot"
	GraphvizDBG(nodesArr, edgesArr, graphfn)
	// Write to files
	edgesfn = prefix + ".edges.LongPath.fq"
	constructdbg.StoreEdgesToFn(edgesfn, edgesArr, false)
	// constructdbg.StoreEdgesToFn(edgesfn, edgesArr, false)
	nodesfn := prefix + ".nodes.LongPath.Arr"
	constructdbg.NodesArrWriter(nodesArr, nodesfn)
}
