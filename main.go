package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/goccy/go-graphviz/cgraph"
	_ "github.com/mattn/go-sqlite3"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"
)

type channelEntry struct {
	packageName        string
	channelName        string
	bundleName         string
	depth              int
	bundleVersion      string
	bundleSkipRange    string
	replacesBundleName string
}

type pkg struct {
	name    string
	bundles map[string]*bundle
}

type bundle struct {
	name              string
	version           string
	packageName       string
	skipRange         string
	minDepth          int
	channels          sets.String
	replaces          sets.String
	skipRangeReplaces sets.String
	isBundlePresent   bool
}

func main() {
	root := cobra.Command{
		Use:   "olm-mermaid-graph",
		Short: "Generate the upgrade graphs from an OLM index",
		Args:  cobra.ExactArgs(0),
		RunE: func(_ *cobra.Command, args []string) error {
			return run()
		},
	}

	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	_, channelEntries, err := loadPackages()
	if err != nil {
		return err
	}

	outputMermaidScript(channelEntries)
	return nil
}

func loadPackages() (map[string]*pkg, []channelEntry, error) {
	pkgs := map[string]*pkg{}
	var channelEntries []channelEntry

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		entryRow := scanner.Text()
		var chanEntry channelEntry
		fields := strings.Split(entryRow, string('|'))
		chanEntry.packageName = fields[0]
		chanEntry.channelName = fields[1]
		chanEntry.bundleName = fields[2]
		chanEntry.depth, _ = strconv.Atoi(fields[3])
		chanEntry.bundleVersion = fields[4]
		chanEntry.bundleSkipRange = fields[5]
		chanEntry.replacesBundleName = fields[6]

		// Get or create package
		p, ok := pkgs[chanEntry.packageName]
		if !ok {
			p = &pkg{
				name:    chanEntry.packageName,
				bundles: make(map[string]*bundle),
			}
		}
		pkgs[chanEntry.packageName] = p

		// Get or create bundle
		bundl, ok := p.bundles[chanEntry.bundleName]
		if !ok {
			bundl = &bundle{
				name:              chanEntry.bundleName,
				packageName:       chanEntry.packageName,
				minDepth:          chanEntry.depth,
				isBundlePresent:   chanEntry.bundleVersion != "",
				channels:          sets.NewString(),
				replaces:          sets.NewString(),
				skipRangeReplaces: sets.NewString(),
			}
			if chanEntry.bundleSkipRange != "" {
				bundl.skipRange = chanEntry.bundleSkipRange
			}
			if chanEntry.bundleVersion != "" {
				bundl.version = chanEntry.bundleVersion
			}
		}
		p.bundles[chanEntry.bundleName] = bundl

		bundl.channels.Insert(chanEntry.channelName)
		if chanEntry.replacesBundleName != "" {
			bundl.replaces.Insert(chanEntry.replacesBundleName)
		}
		if chanEntry.depth < bundl.minDepth {
			bundl.minDepth = chanEntry.depth
		}
		channelEntries = append(channelEntries, chanEntry)
	}
	// validate what we have loaded, as far as graphing concerns
	for _, pkg := range pkgs {
		for _, pkgBundle := range pkg.bundles {
			// check skipRange with semver
			if pkgBundle.skipRange == "" {
				continue
			}
			pSkipRange, err := semver.ParseRange(pkgBundle.skipRange)
			if err != nil {
				log.Warn("invalid skipRange %q for bundle %q: %v -- bundle will not appear in graph\n",
					pkgBundle.skipRange, pkgBundle.name, err)
				delete(pkg.bundles, pkgBundle.name)
				continue
			}
			// check version with semver
			if !pkgBundle.isBundlePresent {
				continue
			}
			cVersion, err := semver.Parse(pkgBundle.version)
			if err != nil {
				log.Warn("invalid version %q for bundle %q: %v -- bundle will not appear in graph\n",
					pkgBundle.version, pkgBundle.name, err)
				delete(pkg.bundles, pkgBundle.name)
				continue
			}
			if pSkipRange(cVersion) {
				pkgBundle.skipRangeReplaces.Insert(pkgBundle.name)
			}
		}
	}

	return pkgs, channelEntries, nil
}

func outputMermaidScript(entries []channelEntry) {
	indent1 := "  "
	indent2 := "    "
	indent3 := "      "

	fmt.Fprintln(os.Stdout, "flowchart LR") // Flowchart left-right header
	fmt.Fprintln(os.Stdout, indent1+"classDef head fill:#ffbfcf;")
	fmt.Fprintln(os.Stdout, indent1+"classDef installed fill:#34ebba;")
	var currPkg string
	var newPkg string
	var currChan string
	var newChan string

	for idx, entry := range entries {
		currChan = entry.channelName + entry.packageName
		if currChan != newChan {
			if newChan != "" {
				fmt.Fprintf(os.Stdout, "\n"+indent2+"end") // end channel graph
			}
		}
		currPkg = entry.packageName
		if currPkg != newPkg {
			if newPkg != "" {
				fmt.Fprintf(os.Stdout, "\n"+indent1+"end") // end pkg graph
			}
			fmt.Fprintf(os.Stdout, "\n"+indent1+"subgraph "+entry.packageName) // per package graph
		}
		if entry.depth == 0 {
			fmt.Fprintf(os.Stdout, "\n"+indent2+"subgraph "+entry.channelName+" channel") // per channel graph
			fmt.Fprintf(os.Stdout, "\n"+indent3+strconv.Itoa(idx)+"("+entry.bundleVersion+"):::head")
			newChan = currChan
		} else {
			// catch empty bundleVersion fields, they create a Mermaid syntax error
			bundleVersion := entry.bundleVersion
			if bundleVersion == "" {
				bundleVersion = "x.y.z"
			}
			fmt.Fprintf(os.Stdout, " --> "+strconv.Itoa(idx)+"("+bundleVersion+")")
		}
		newPkg = currPkg
		if idx == len(entries)-1 {
			fmt.Fprintf(os.Stdout, "\n"+indent2+"end") // end channel graph
			fmt.Fprintf(os.Stdout, "\n"+indent1+"end") // end pkg graph
		}
	}
}

func makeGraphDot(graph *cgraph.Graph, pkgs map[string]*pkg) error {
	for _, p := range pkgs {
		pGraph := graph.SubGraph(fmt.Sprintf("cluster_%s", p.name), 1)
		pGraph.SetLabel(fmt.Sprintf("package: %s", p.name))

		for _, b := range p.bundles {
			nodeName := fmt.Sprintf("%s_%s", p.name, b.name)
			node, err := pGraph.CreateNode(nodeName)
			if err != nil {
				return err
			}
			node.SetShape("record")
			node.SetWidth(4)
			node.SetLabel(fmt.Sprintf("{%s|{channels|{%s}}}", b.name, strings.Join(b.channels.List(), "|")))
			if !b.isBundlePresent {
				node.SetStyle(cgraph.DashedNodeStyle)
			}
			if b.minDepth == 0 {
				node.SetPenWidth(4.0)
			}
		}

		for _, pb := range p.bundles {
			pName := fmt.Sprintf("%s_%s", p.name, pb.name)
			parent, err := pGraph.Node(pName)
			if err != nil {
				return err
			}
			for _, cb := range pb.replaces.List() {
				cName := fmt.Sprintf("%s_%s", p.name, cb)
				child, err := pGraph.Node(cName)
				if err != nil {
					return err
				}
				edgeName := fmt.Sprintf("replaces_%s_%s", parent.Name(), child.Name())
				if _, err := pGraph.CreateEdge(edgeName, parent, child); err != nil {
					return err
				}
			}
			for _, cb := range pb.skipRangeReplaces.List() {
				cName := fmt.Sprintf("%s_%s", p.name, cb)
				child, err := pGraph.Node(cName)
				if err != nil {
					return err
				}

				edgeName := fmt.Sprintf("skipRange_%s_%s", parent.Name(), child.Name())
				if !pb.replaces.Has(cb) {
					edge, err := pGraph.CreateEdge(edgeName, parent, child)
					if err != nil {
						return err
					}
					edge.SetStyle(cgraph.DashedEdgeStyle)
				}
			}
		}
	}
	return nil
}
