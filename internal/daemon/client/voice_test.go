package client

import (
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/testutil"
)

type voiceRPC struct {
	errMsg string
	calls  []string
}

func (v *voiceRPC) Ping(_ *daemon.Empty, resp *daemon.PongResp) error {
	resp.OK = true
	return nil
}

func (v *voiceRPC) VoiceStart(_ *daemon.Empty, resp *daemon.StatusResp) error {
	v.calls = append(v.calls, "start")
	resp.OK, resp.Error = v.errMsg == "", v.errMsg
	return nil
}

func (v *voiceRPC) VoiceStop(_ *daemon.Empty, resp *daemon.StatusResp) error {
	v.calls = append(v.calls, "stop")
	resp.OK, resp.Error = v.errMsg == "", v.errMsg
	return nil
}

func (v *voiceRPC) VoiceStatus(_ *daemon.Empty, resp *daemon.VoiceStatusResp) error {
	resp.Available = true
	resp.Recording = true
	resp.Hotkey = "right_option"
	resp.LastTaskID = "t1"
	resp.Error = v.errMsg
	return nil
}

func startVoiceRPC(t *testing.T, v *voiceRPC) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "v.sock")
	ln, err := net.Listen("unix", sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	server := rpc.NewServer()
	testutil.NoError(t, server.RegisterName("Daemon", v))
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				prefix := make([]byte, 1)
				if _, err := io.ReadFull(conn, prefix); err != nil {
					return
				}
				server.ServeCodec(jsonrpc.NewServerCodec(conn))
			}()
		}
	}()
	c, err := Connect(sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClient_Voice(t *testing.T) {
	v := &voiceRPC{}
	c := startVoiceRPC(t, v)

	testutil.NoError(t, c.VoiceStart())
	testutil.NoError(t, c.VoiceStop())
	testutil.DeepEqual(t, v.calls, []string{"start", "stop"})

	st, err := c.VoiceStatus()
	testutil.NoError(t, err)
	testutil.True(t, st.Available)
	testutil.True(t, st.Recording)
	testutil.Equal(t, st.Hotkey, "right_option")
	testutil.Equal(t, st.LastTaskID, "t1")
}

func TestClient_VoiceDaemonErrors(t *testing.T) {
	c := startVoiceRPC(t, &voiceRPC{errMsg: "voice off"})

	testutil.Equal(t, c.VoiceStart().Error(), "voice off")
	testutil.Equal(t, c.VoiceStop().Error(), "voice off")
	_, err := c.VoiceStatus()
	testutil.Equal(t, err.Error(), "voice off")
}

func TestClient_VoiceTransportErrors(t *testing.T) {
	c := startVoiceRPC(t, &voiceRPC{})
	_ = c.Close()

	testutil.Error(t, c.VoiceStart())
	testutil.Error(t, c.VoiceStop())
	_, err := c.VoiceStatus()
	testutil.Error(t, err)
}
