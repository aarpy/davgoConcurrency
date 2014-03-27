package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"
)

type node struct {
	User        string
	ListenPort  int
	ConnectPort int
	Bot         bool
}

func newNode(user string, lport, cport int, bot bool) *node {
	return &node{user, lport, cport, bot}
}

func (n *node) String() string {
	return fmt.Sprintf("%s_%d_%d", n.User, n.ListenPort, n.ConnectPort)
}

func rndNode(nodes []*node, maxDepth int) *node {
	nodeCnt := len(nodes)
	max := nodeCnt
	if maxDepth > 0 {
		max = (max % maxDepth)
		if max == 0 {
			max = 1
		}
	}
	i := rand.Intn(max)
	return nodes[i]
}

type gviz struct {
	Name  string
	Edges []string
}

func newGviz(graphName string) *gviz {
	return &gviz{graphName, make([]string, 10)}
}

func (gv *gviz) AddEdge(from, to string) {
	edge := fmt.Sprintf("\t%s -> %s;\n", from, to)
	gv.Edges = append(gv.Edges, edge)
}

func (gv *gviz) Write(fileName string) error {
	edges := strings.Join(gv.Edges, "")
	fileText := fmt.Sprintf("digraph %s {\n\tranksep=3\n%s\n}", gv.Name, edges)
	return ioutil.WriteFile(fileName, []byte(fileText), 0666)
}

func startNode(n *node) (*exec.Cmd, error) {
	user := fmt.Sprintf("-u=%s", n.User)
	lport := fmt.Sprintf(`-l=:%d`, n.ListenPort)
	cport := ""
	if n.ConnectPort > 0 {
		cport = fmt.Sprintf(`-a=localhost:%d`, n.ConnectPort)
	}
	bot := ""
	if n.Bot {
		bot = "-b"
	}
	cmd := exec.Command("./mesh", user, lport, cport, bot)
	if n.User == "root" {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd, cmd.Start()
}

func main() {
	var nodeCnt int
	flag.IntVar(&nodeCnt, "n", 10, "number of nodes")
	var maxDepth int
	flag.IntVar(&maxDepth, "d", -1, "max graph depth (-1 == unlimited)")
	flag.Parse()

	rand.Seed(time.Now().UTC().UnixNano())

	var nodes []*node
	nodes = append(nodes, newNode("root", 4242, 0, false))

	gv := newGviz("Mesh")

	// generate a DAG of peers chatting with each other
	for i := 0; i < nodeCnt; i++ {
		user := fmt.Sprintf("peer%d", i)
		lport := 4242 + i
		parent := rndNode(nodes[:], maxDepth)
		child := newNode(user, lport, parent.ListenPort, true)
		nodes = append(nodes, child)
		gv.AddEdge(child.String(), parent.String())
	}

	gv.Write("mesh.dot")
	cmd := exec.Command("dot", "-Tpng", "-o mesh.png", "mesh.dot")
	cmd.Run()

	var cmds []*exec.Cmd
	for _, node := range nodes {
		<-time.After(time.Duration(rand.Intn(200)) * time.Millisecond)
		cmd, err := startNode(node)
		if err != nil {
			fmt.Println(err)
		} else {
			cmds = append(cmds, cmd)
		}
	}

	for _, cmd := range cmds {
		cmd.Wait()
	}
}
