package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"

	"github.com/mudesheng/GA/constructcf"
	//"./constructdbg"
	//"./findPath"

	"github.com/jwaldrip/odin/cli"
	"github.com/mudesheng/GA/constructdbg"
	"github.com/mudesheng/GA/findPath"
	"github.com/mudesheng/GA/mapDBG"
)

//"./mapDBG"
// "strconv"
//	"fmt"

const Kmerdef = 203

type GAArgs struct {
	cfg    string
	kmer   int
	prefix string
	numCPU int
	cfSize int64
}

var app = cli.New("1.0.0", "Graph Assembler for complex genome", func(c cli.Command) {})

//var gaargs GAArgs

func init() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	app.DefineStringFlag("C", "ga.cfg", "configure file")
	app.DefineIntFlag("K", Kmerdef, "kmer length")
	// app.DefineStringFlag("p", "K"+strconv.Itoa(Kmerdef), "prefix of the output file")
	app.DefineStringFlag("p", "./test/t20150708/K203", "prefix of the output file")
	app.DefineIntFlag("t", 1, "number of CPU used")
	ccf := app.DefineSubCommand("ccf", "construct cukcoofilter", constructcf.CCF)
	{
		ccf.DefineInt64Flag("S", 0, "the Size number of items cuckoofilter set")
	}
	cdbg := app.DefineSubCommand("cdbg", "construct De bruijn Graph", constructdbg.CDBG)
	{
		cdbg.DefineIntFlag("tipMaxLen", Kmerdef*2, "Maximum tip length")
	}

	smfy := app.DefineSubCommand("smfy", "find Illumina reads path and simplify De bruijn Graph", constructdbg.Smfy)
	{
		smfy.DefineIntFlag("tipMaxLen", Kmerdef*2, "Maximum tip length")
		smfy.DefineIntFlag("WinSize", 10, "th size of sliding window for DBG edge Sample")
		smfy.DefineIntFlag("MaxNGSReadLen", 550, "Max NGS Read Length")
	}
	// mapping long read to the DBG
	mapDBG := app.DefineSubCommand("mapDBG", "mapping long read to the DBG", mapDBG.MapDBG)
	{
		mapDBG.DefineIntFlag("Seed", 15, "the seek length(must <=16)")
		mapDBG.DefineIntFlag("Width", 5, "band width for found min kmer")
	}
	// find short read mapping
	fspath := app.DefineSubCommand("fspath", "Parse short read path", findPath.FSpath)
	{
		fspath.DefineIntFlag("tipMaxLen", Kmerdef*2, "Maximum tip length")
	}
	// find long read mapping
	flpath := app.DefineSubCommand("flpath", "Parse long read path", findPath.FLpath)
	{
		flpath.DefineIntFlag("tipMaxLen", Kmerdef*2, "Maximum tip length")
	}
	// merge find short and long read mapping path
	fpath := app.DefineSubCommand("fpath", "Merge Parse short and long read path", findPath.Fpath)
	{
		fpath.DefineIntFlag("tipMaxLen", Kmerdef*2, "Maximum tip length")
	}
}

func main() {
	app.Start()
}
