package cuckoofilterDBGSample

// #include<stdint.h>
/*
uint16_t CompareAndSwapUint16(uint16_t *addr, uint16_t old, uint16_t new)
{
    return __sync_val_compare_and_swap(addr, old, new);
}*/
import "C"

import (
	"crypto/sha1"
	"log"
	"math"

	"github.com/mudesheng/GA/constructdbg"
	//"log"
	//"sync/atomic"
	//"strings"

	// "syscall"
	"unsafe"
)

const (
	NUM_FP_BITS = 14     // number of fingerprint  bits occpied
	NUM_C_BITS  = 2      // number of count equal sizeof(uint16)*8 - NUM_FP_BITS
	FPMASK      = 0x3FFF // mask other info only set fingerprint = (1<<NUM_FP_BITS) -1
	CMASK       = 0x3    // set count bits field = (1<<NUM_C_BITS) -1
	MAX_C       = (1 << NUM_C_BITS) - 1
)

const BucketSize = 4
const MaxLoad = 0.95
const KMaxCount = 512
const MAXFREQ = math.MaxUint16

//func CompareAndSwapUint16(addr *uint16, old uint16, new uint16) (swapped bool)

type CFItem uint16

type DBGKmer struct {
	Item CFItem
	Freq uint16
	ID   constructDBG.DBG_MAX_INT
	Pos  uint32
}

/* type cfitem struct {
  fingerprint uint16:NUM_FP_BITS
  count uint16:NUM_C_BITS
} */

type Bucket struct {
	Bkt [BucketSize]DBGKmer
}

type CuckooFilter struct {
	Hash     []Bucket
	numItems uint64
	Kmerlen  int
}

var countItems uint64

func upperpower2(x uint64) uint64 {
	x--
	x |= x >> 1
	x |= x >> 2
	x |= x >> 4
	x |= x >> 8
	x |= x >> 16
	x |= x >> 32
	x++

	return x
}

// "MakeCuckooFilter is for construct Cuckoo Filter"
func MakeCuckooFilter(maxNumKeys uint64, kmerLen int) (cf CuckooFilter) {
	numBuckets := upperpower2(maxNumKeys) / BucketSize
	/*frac := float64(maxNumKeys) / numBuckets / BucketSize
	if frac > MaxLoad {
		numBuckets <<= 1
	}*/

	cf.Hash = make([]Bucket, numBuckets)
	cf.numItems = numBuckets * BucketSize
	cf.Kmerlen = kmerLen

	return cf

}

func (cf CuckooFilter) IndexHash(v uint64) uint64 {
	v >>= NUM_FP_BITS

	return v % uint64(len(cf.Hash))
}

func FingerHash(v uint64) uint64 {
	v &= FPMASK
	return v
}

func (cf CuckooFilter) AltIndex(index uint64, finger uint64) uint64 {
	index ^= finger

	return index % uint64(len(cf.Hash))
}

/*func combineCFItem(fp uint64, count uint64) (cfi CFItem) {
	if count > MAX_C {
		panic("count bigger than CFItem allowed")
	}
	cfi = CFItem(fp)
	cfi <<= NUM_C_BITS
	cfi |= CFItem(count)
	//fmt.Printf("fp: %d, count: %d, cfi: %d\n", fp, count, uint16(cfi))
	return cfi
}

func (cfi CFItem) GetCount() uint16 {
	return uint16(cfi) & CMASK
}

func (cfi *CFItem) setCount(count uint16) {
	if count > MAX_C {
		panic("count bigger than CFItem allowed")
	}
	nc := uint16(*cfi) >> NUM_C_BITS
	nc <<= NUM_C_BITS
	nc |= count

	*cfi = CFItem(nc)
} */

func (dbgK DBGKmer) GetFinger() uint16 {
	return uint16(dbgK.Item >> NUM_C_BITS)
}

func (dbgK DBGKmer) EqualFP(sec DBGKmer) bool {
	if dbgK.GetFinger() == sec.GetFinger() {
		return true
	} else {
		return false
	}
}

// return oldcount, oldfinger, added
/*func (cfi *CFItem) AddCount() (int, uint64, bool) {
	for {
		oc := *cfi
		count := oc.GetCount()
		if count < MAX_C {
			nc := oc
			nc.setCount(count + 1)
			if C.CompareAndSwapUint16((*C.uint16_t)(cfi), C.uint16_t(oc), C.uint16_t(nc)) == C.uint16_t(oc) {
				return int(count), 0, true
			}
		} else {
			return MAX_C, 0, true
		}
	}
}*/

func (b Bucket) Contain(fingerprint uint16) (DBGKmer, bool) {
	for i, item := range b.Bkt {
		fp := item.GetFinger()
		if fp == fingerprint {
			return b.Bkt[i], true
		}
	}
	return nil, false
}

func (b *Bucket) AddBucket(dbgK DBGKmer, kickout bool) (DBGKmer, bool) {
	for i := 0; i < BucketSize; i++ {
		//fmt.Printf("i: %d\n", i)
		if b.Bkt[i].GetCount() == 0 {
			b.Bkt[i] = dbgK
			return nil, true
		} else {
			if b.Bkt[i].EqualFP(dbgK) {
				log.Fatalf("[AddBucket] found conflicting DBG Kmer, please increase size cuckoofilter of DBG Sample\n")
			}
		}
	}

	//fmt.Printf("kikcout: %t", kickout)
	if kickout {
		ci := dbgK.GetFinger() & CMASK
		old = b.Bkt[ci]
		b.Bkt[ci] = dbgK
		return old, true
	} else {
		return nil, false
	}
}

// return last count of kmer and if successed added
func (cf CuckooFilter) Add(index uint64, dbgK DBGKmer) bool {
	ci := index
	gK := dbgK
	for count := 0; count < KMaxCount; count++ {
		kickout := count > 0
		b := &cf.Hash[ci]
		old, added := b.AddBucket(gK, kickout)
		if added == true && old.GetCount() == 0 {
			return true
		}
		if kickout && old.GetCount() > 0 {
			gK = old
		}
		//fmt.Printf("cycle : %d\n", count)

		ci = cf.AltIndex(ci, gk.GetFinger())
	}
	return false
}

func hk2uint64(hk [sha1.Size]byte) (v uint64) {
	for i := 0; i <= len(hk)-8; i += 8 {
		hkp := unsafe.Pointer(&hk[i])
		t := (*uint64)(hkp)
		v ^= *t
	}
	if len(hk)%8 > 0 {
		hkp := unsafe.Pointer(&hk[len(hk)-8])
		t := (*uint64)(hkp)
		v ^= *t
	}

	return v
}

// return last count of kmer fingerprint and have been successed inserted
func (cf CuckooFilter) Insert(kb []byte, id constructdbg.DBG_MAX_INT, pos uint32) bool {
	hk := sha1.Sum(kb)
	v := hk2uint64(hk)
	//fmt.Printf("%v\t", v)
	index := cf.IndexHash(v)
	fingerprint := FingerHash(v)
	var dbgK DBGKmer
	dbgK.Item = CFItem(fingerprint << NUM_C_BITS)
	dbgK.Item |= CFItem(1)
	dbgK.ID = id
	dbgK.Pos = pos
	//fmt.Printf("[cf.Insert]%v\t%v\n", index, fingerprint)
	//fmt.Printf(" sizeof cuckoofilter.Hash[0] : %d\n", unsafe.Sizeof(cf.Hash[0]))

	return cf.Add(index, dbgK)
}

func (cf CuckooFilter) Lookup(kb []byte) DBGKmer {
	hk := sha1.Sum(kb)
	v := hk2uint64(hk)
	index := cf.IndexHash(v)
	fingerprint := FingerHash(v)
	var dbgK DBGKmer
	if dbgK, c := cf.Hash[index].Contain(uint16(fingerprint)); c {
		return dbgK
	}
	index = cf.AltIndex(index, fingerprint)
	if dbgK, fc := cf.Hash[index].Contain(uint16(fingerprint)); c {
		return dbgK
	}
	return nil
}

/*func (cf CuckooFilter) GetCount(kb []byte) uint16 {
	hk := sha1.Sum(kb)
	v := hk2uint64(hk)
	index := cf.IndexHash(v)
	fingerprint := FingerHash(v)
	for _, item := range cf.Hash[index].Bkt {
		// fmt.Printf("index: %v, finger: %v\n", index, item.GetFinger())
		if item.GetFinger() == uint16(fingerprint) {
			return item.GetCount()
		}
	}
	// if not return , find another position
	index = cf.AltIndex(index, fingerprint)
	for _, item := range cf.Hash[index].Bkt {
		// fmt.Printf("index: %v, finger: %v\n", index, item.GetFinger())
		if item.GetFinger() == uint16(fingerprint) {
			return item.GetCount()
		}
	}

	// not found in the CuckooFilter
	panic("not found in the CuckooFilter")
}

// allow function return zero version if kb not found in the CuckooFilter
func (cf CuckooFilter) GetCountAllowZero(kb []byte) uint16 {
	hk := sha1.Sum(kb)
	v := hk2uint64(hk)
	// fmt.Printf("[GetCountAllowZero] len(cf.Hash): %d\n", len(cf.Hash))
	// fmt.Printf("[GetCountAllowZero] cf.Hash[0]: %v\n", cf.Hash[0])
	index := cf.IndexHash(v)
	fingerprint := FingerHash(v)
	for _, item := range cf.Hash[index].Bkt {
		// fmt.Printf("index: %v, finger: %v\n", index, item.GetFinger())
		if item.GetFinger() == uint16(fingerprint) {
			return item.GetCount()
		}
	}
	// if not return , find another position
	index = cf.AltIndex(index, fingerprint)
	for _, item := range cf.Hash[index].Bkt {
		// fmt.Printf("index: %v, finger: %v\n", index, item.GetFinger())
		if item.GetFinger() == uint16(fingerprint) {
			return item.GetCount()
		}
	}

	// not found in the CuckooFilter, return zero
	// panic("not found in the CuckooFilter")
	return 0
}

func (cf CuckooFilter) GetStat() {
	var ca [4]int
	for _, b := range cf.Hash {
		for _, e := range b.Bkt {
			count := e.GetCount()
			ca[count]++
		}
	}
	fmt.Printf("count statisticas : %v\n", ca)
	fmt.Printf("cuckoofilter numItems : %d, countItems: %d, load: %f\n", cf.numItems, countItems, float64(countItems)/float64(cf.numItems))
}

func (cf CuckooFilter) MmapWriter(cfmmapfn string) error {
	cfmmapfp, err := os.Create(cfmmapfn)
	if err != nil {
		return err
	}
	defer cfmmapfp.Close()

	// write cuckoofilter to the memory map file
	enc := gob.NewEncoder(cfmmapfp)
	err = enc.Encode(cf)
	if err != nil {
		return err
	}
// n, err := cfmmapfp.Write([]byte(cf.Hash[0].Bkt[0]))
// if err != nil {
// 	return err
// }
// if n != cf.numItems*unsafe.Sizeof(bucket) {
// 	log.Fatal("write byte number is not equal cf size")
// }

	return nil
}

func (cf CuckooFilter) WriteCuckooFilterInfo(cfinfofn string) error {
	cfinfofp, err := os.Create(cfinfofn)
	if err != nil {
		return err
	}
	defer cfinfofp.Close()

	_, err = cfinfofp.WriteString(fmt.Sprintf("numItems\t%d\n", cf.numItems))
	if err != nil {
		return err
	}
	_, err = cfinfofp.WriteString(fmt.Sprintf("Kmerlen\t%d\n", cf.Kmerlen))
	if err != nil {
		return err
	}

	return nil
}

func RecoverCuckooFilterInfo(cfinfofn string) (cf CuckooFilter, err error) {
	var cfinfofp *os.File
	cfinfofp, err = os.Open(cfinfofn)
	if err != nil {
		return cf, err
	}
	defer cfinfofp.Close()
	cfinfobuf := bufio.NewReader(cfinfofp)
	var line string
	// eof := false
	line, err = cfinfobuf.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			// eof = true
			err = nil
		} else {
			return cf, err
		}
	}
	// var num int
	// fmt.Printf("[RecoverCuckooFilterInfo] line: %s\n", line)
	_, err = fmt.Sscanf(line, "numItems\t%d\n", &cf.numItems)
	if err != nil {
		return cf, err
	}
	line, err = cfinfobuf.ReadString('\n')
	// fmt.Printf("[RecoverCuckooFilterInfo] line: %s\n", line)
	_, err = fmt.Sscanf(line, "Kmerlen\t%d\n", &cf.Kmerlen)
	if err != io.EOF {
		return cf, err
	} else {
		err = nil
	}

	return
}

func MmapReader(cfmmapfn string) (cf CuckooFilter, err error) {
	// if cf.numItems <= 0 {
	// 	log.Fatal("CuckooFilter number is <=0, please check")
	// }
	cfmmapfp, err := os.Open(cfmmapfn)
	if err != nil {
		return cf, err
	}
	defer cfmmapfp.Close()
	dec := gob.NewDecoder(cfmmapfp)
	err = dec.Decode(&cf)
	if err != nil {
		log.Fatalf("[MmapReader] err : %v\n", err)
	}
	// fmt.Printf("[MmapReader] cf.Kmerlen: %d\n", cf.Kmerlen)
	// fmt.Printf("[MmapReader] len(cf.Hash): %d\n", len(cf.Hash))
	// fmt.Printf("[MmapReader] cf.Hash[0]: %d\n", cf.Hash[0])

	// mmap, err := syscall.Mmap(cfmmapfp.Fd(), 0, cf.numItems*unsafe.Sizeof(cf.Hash[0].Bkt[0]), syscall.PROT_READ, syscall.MAP_SHARED)
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// cf.Hash = []Bucket(mmap)

	return cf, nil
}*/