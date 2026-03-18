package main

import (
	"fmt"
	"net"
	"net/rpc/jsonrpc"
	"os"
	"strconv"

	"github.com/drn/argus/internal/daemon"
)

// runKBCommand handles: argus kb search|ingest|list|status
func runKBCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: argus kb <search|ingest|list|status> [args...]")
		os.Exit(1)
	}

	sockPath := daemon.DefaultSocketPath()

	switch args[0] {
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: argus kb search <query> [limit]")
			os.Exit(1)
		}
		query := args[1]
		limit := 10
		if len(args) >= 3 {
			if n, err := strconv.Atoi(args[2]); err == nil {
				limit = n
			}
		}
		resp := callKBSearch(sockPath, query, limit)
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "search error: %s\n", resp.Error)
			os.Exit(1)
		}
		if len(resp.Results) == 0 {
			fmt.Println("No results found.")
			return
		}
		for i, r := range resp.Results {
			fmt.Printf("%d. [%s] %s\n   %s\n\n", i+1, r.Tier, r.Title, r.Path)
			if r.Snippet != "" {
				fmt.Printf("   %s\n\n", r.Snippet)
			}
		}

	case "list":
		prefix := ""
		limit := 100
		for i := 1; i < len(args); i++ {
			switch {
			case args[i] == "--prefix" && i+1 < len(args):
				prefix = args[i+1]
				i++
			case args[i] == "--limit" && i+1 < len(args):
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					limit = n
				}
				i++
			default:
				prefix = args[i]
			}
		}
		resp := callKBList(sockPath, prefix, limit)
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "list error: %s\n", resp.Error)
			os.Exit(1)
		}
		if len(resp.Documents) == 0 {
			fmt.Println("No documents found.")
			return
		}
		for _, d := range resp.Documents {
			fmt.Printf("%-60s [%s] %d words\n", d.Path, d.Tier, d.WordCount)
		}

	case "ingest":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: argus kb ingest <file>")
			os.Exit(1)
		}
		path := args[1]
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			os.Exit(1)
		}
		resp := callKBIngest(sockPath, path, string(data))
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "ingest error: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Printf("Ingested: %s\n", path)

	case "status":
		resp := callKBStatus(sockPath)
		fmt.Printf("Documents : %d\n", resp.DocumentCount)
		if resp.VaultPath != "" {
			fmt.Printf("Vault     : %s\n", resp.VaultPath)
		} else {
			fmt.Println("Vault     : (not configured)")
		}
		if resp.Port != 0 {
			fmt.Printf("MCP port  : %d\n", resp.Port)
		} else {
			fmt.Println("MCP port  : (not running)")
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown kb subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func kbRPCClient(sockPath string) (*rpcConn, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon: %w", err)
	}
	if _, err := conn.Write([]byte("R")); err != nil {
		conn.Close()
		return nil, err
	}
	return &rpcConn{conn: conn, client: jsonrpc.NewClient(conn)}, nil
}

type rpcConn struct {
	conn   net.Conn
	client interface {
		Call(serviceMethod string, args interface{}, reply interface{}) error
		Close() error
	}
}

func callKBSearch(sockPath, query string, limit int) daemon.KBSearchResp {
	rc, err := kbRPCClient(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer rc.conn.Close()

	var resp daemon.KBSearchResp
	if err := rc.client.Call("Daemon.KBSearch", &daemon.KBSearchReq{Query: query, Limit: limit}, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "rpc error: %v\n", err)
		os.Exit(1)
	}
	return resp
}

func callKBList(sockPath, prefix string, limit int) daemon.KBListResp {
	rc, err := kbRPCClient(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer rc.conn.Close()

	var resp daemon.KBListResp
	if err := rc.client.Call("Daemon.KBList", &daemon.KBListReq{Prefix: prefix, Limit: limit}, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "rpc error: %v\n", err)
		os.Exit(1)
	}
	return resp
}

func callKBIngest(sockPath, path, content string) daemon.KBIngestResp {
	rc, err := kbRPCClient(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer rc.conn.Close()

	var resp daemon.KBIngestResp
	if err := rc.client.Call("Daemon.KBIngest", &daemon.KBIngestReq{Path: path, Content: content}, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "rpc error: %v\n", err)
		os.Exit(1)
	}
	return resp
}

func callKBStatus(sockPath string) daemon.KBStatusResp {
	rc, err := kbRPCClient(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer rc.conn.Close()

	var resp daemon.KBStatusResp
	if err := rc.client.Call("Daemon.KBStatus", &daemon.Empty{}, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "rpc error: %v\n", err)
		os.Exit(1)
	}
	return resp
}
