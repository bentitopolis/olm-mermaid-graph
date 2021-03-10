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

var indent1 = "  "
var indent2 = "    "
var indent3 = "      "

func main() {
	var pkgToGraph string
	root := cobra.Command{
		Use:   "olm-mermaid-graph",
		Short: "Generate the upgrade graphs from an OLM index",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				pkgToGraph = args[0]
			}
			return run(pkgToGraph)
		},
	}

	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run(pkgToGraph string) error {
	pkgs, err := loadPackages(pkgToGraph)
	if err != nil {
		return err
	}

	outputMermaidScript(pkgs)
	return nil
}

func loadPackages(pkgToGraph string) (map[string]*pkg, error) {
	pkgs := map[string]*pkg{}

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

		if pkgToGraph != "" && chanEntry.packageName != pkgToGraph {
			continue
		}
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
			} else {
				// catch empty version field because empty '()' are a Mermaid syntax error
				bundl.version = "x.y.z"
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
	}
	for _, p := range pkgs {
		for _, pb := range p.bundles {
			if pb.skipRange == "" {
				continue
			}
			pSkipRange, err := semver.ParseRange(pb.skipRange)
			if err != nil {
				fmt.Errorf("invalid range %q for bundle %q: %v", pb.skipRange, pb.name, err)
				continue
			}
			for _, cb := range p.bundles {
				if !cb.isBundlePresent {
					continue
				}
				cVersion, err := semver.Parse(cb.version)
				if err != nil {
					fmt.Errorf("invalid version %q for bundle %q: %v", cb.version, cb.name, err)
					continue
				}
				if pSkipRange(cVersion) {
					pb.skipRangeReplaces.Insert(cb.name)
				}
			}
		}
	}

	return pkgs, nil
}

func outputMermaidScript(pkgs map[string]*pkg) {
	graphHeader()
	for _, pkg := range pkgs {
		allBundleChannels := sets.NewString()                     // we want subgraph organized by channel
		fmt.Fprintf(os.Stdout, "\n"+indent1+"subgraph "+pkg.name) // per package graph
		for _, bundle := range pkg.bundles {
			allBundleChannels = bundle.channels.Union(allBundleChannels)
		}
		for _, channel := range allBundleChannels.List() {
			fmt.Fprintf(os.Stdout, "\n"+indent2+"subgraph "+channel+" channel") // per channel graph
			replaceSet := sets.NewString()
			for _, bundle := range pkg.bundles {
				if bundle.channels.Has(channel) {
					// if no replaces edges, just write the node
					if bundle.replaces.Len() == 0 && bundle.skipRangeReplaces.Len() == 0 {
						if bundle.minDepth == 0 {
							replaceSet.Insert(bundle.name + "-" + channel + "(" + bundle.version + "):::head")
						} else {
							replaceSet.Insert(bundle.name + "-" + channel + "(" + bundle.version + ")")
						}
					}
					for _, replace := range bundle.replaces.List() {
						if bundle.minDepth == 0 {
							replaceSet.Insert(bundle.name + "-" + channel + "(" + bundle.version + "):::head" +
								" --> " + replace + "-" + channel + "(" + pkg.bundles[replace].version + ")")
						} else {
							replaceSet.Insert(bundle.name + "-" + channel + "(" + bundle.version + ")" +
								" --> " + replace + "-" + channel + "(" + pkg.bundles[replace].version + ")")
						}
					} // end bundle replaces edge graphing
					for _, skipReplace := range bundle.skipRangeReplaces.List() {
						if !bundle.replaces.Has(skipReplace) {
							if bundle.minDepth == 0 {
								fmt.Fprintf(os.Stdout, "\n"+indent3+bundle.name+"-"+
									channel+"("+bundle.version+"):::head"+" o--o | "+bundle.skipRange+" | "+
									skipReplace+"-"+channel+"("+pkg.bundles[skipReplace].version+")")
							} else {
								fmt.Fprintf(os.Stdout, "\n"+indent3+bundle.name+"-"+
									channel+"("+bundle.version+")"+" o--o | "+bundle.skipRange+" | "+
									skipReplace+"-"+channel+"("+pkg.bundles[skipReplace].version+")")
							}
						}
					} // end bundle skipReplaces edge graphing
				}
			}
			for _, replaceLine := range replaceSet.List() {
				fmt.Fprintf(os.Stdout, "\n"+indent3+replaceLine)
			}
			fmt.Fprintf(os.Stdout, "\n"+indent2+"end") // end channel graph
		} // end per channel loop
		fmt.Fprintf(os.Stdout, "\n"+indent1+"end") // end pkg graph
	} // end package loop
}

func graphHeader() {
	fmt.Fprintln(os.Stdout, "flowchart LR") // Flowchart left-right header
	fmt.Fprintln(os.Stdout, indent1+"classDef head fill:#ffbfcf;")
	fmt.Fprintln(os.Stdout, indent1+"classDef installed fill:#34ebba;")
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
