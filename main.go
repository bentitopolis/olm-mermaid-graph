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
	pkgs, err := loadPackages()
	if err != nil {
		return err
	}

	outputMermaidScript(pkgs)
	//g := graphviz.New()
	//defer g.Close()
	//
	//graph, err := g.Graph()
	//if err != nil {
	//	return err
	//}
	//
	//defer graph.Close()
	//
	//if err := makeGraphDot(graph, pkgs); err != nil {
	//	return err
	//}
	//
	//dotFile := filepath.Join(filepath.Dir("."), fmt.Sprintf("%s.dot", filepath.Base("graph_tmp")))
	//if err := g.RenderFilename(graph, graphviz.XDOT, dotFile); err != nil {
	//	return err
	//}
	return nil
}

func loadPackages() (map[string]*pkg, error) {
	pkgs := map[string]*pkg{}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		entryRow := scanner.Text()
		var e channelEntry
		//if err := entryRow.Scan(&e.packageName, &e.channelName, &e.bundleName, &e.depth, &e.bundleVersion, &e.bundleSkipRange, &e.replacesBundleName); err != nil {
		//	return nil, err
		//}
		fields := strings.Split(entryRow, string('|'))
		e.packageName = fields[0]
		e.channelName = fields[1]
		e.bundleName = fields[2]
		e.depth, _ = strconv.Atoi(fields[3])
		e.bundleVersion = fields[4]
		e.bundleSkipRange = fields[5]
		e.replacesBundleName = fields[6]

		// Get or create package
		p, ok := pkgs[e.packageName]
		if !ok {
			p = &pkg{
				name:    e.packageName,
				bundles: make(map[string]*bundle),
			}
		}
		pkgs[e.packageName] = p

		// Get or create bundle
		b, ok := p.bundles[e.bundleName]
		if !ok {
			b = &bundle{
				name:              e.bundleName,
				packageName:       e.packageName,
				minDepth:          e.depth,
				isBundlePresent:   e.bundleVersion != "",
				channels:          sets.NewString(),
				replaces:          sets.NewString(),
				skipRangeReplaces: sets.NewString(),
			}
			if e.bundleSkipRange != "" {
				b.skipRange = e.bundleSkipRange
			}
			if e.bundleVersion != "" {
				b.version = e.bundleVersion
			}
		}
		p.bundles[e.bundleName] = b

		b.channels.Insert(e.channelName)
		if e.replacesBundleName != "" {
			b.replaces.Insert(e.replacesBundleName)
		}
		if e.depth < b.minDepth {
			b.minDepth = e.depth
		}

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

	return pkgs, nil
}

func outputMermaidScript(pkgs map[string]*pkg) {
	fmt.Fprintln(os.Stdout, "graph TD")
	fmt.Fprintln(os.Stdout, "	classDef head fill:#ffbfcf;")
	fmt.Fprintln(os.Stdout, "	classDef installed fill:#34ebba;")
	cnt := 0
	for _, pkg := range pkgs {
		fmt.Fprintf(os.Stdout, "	subgraph %s\n", pkg.name)
		fmt.Fprintf(os.Stdout, "	%s_A(v0.0.1):::installed --> %s_B(v0.0.2)\n", pkg.name, pkg.name)
		fmt.Fprintf(os.Stdout, "	end\n")
		if cnt > 3 {
			break
		}
		cnt++
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
