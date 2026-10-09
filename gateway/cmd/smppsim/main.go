// Command smppsim runs SMPP simulators for testing and load tests.
//
//	smppsim vendor -listen :9000 [-fail 0.1] [-dlr-delay 500ms] [-mode ok|reject|throttle|noack]
//	smppsim client -addr host:2775 -user U -pass P [-n 1000] [-tps 100] [-from Brand] [-prefix 92300]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/sim"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: smppsim vendor|client [flags]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch os.Args[1] {
	case "vendor":
		fs := flag.NewFlagSet("vendor", flag.ExitOnError)
		listen := fs.String("listen", ":9000", "listen address")
		mode := fs.String("mode", "ok", "ok, reject, throttle or noack")
		fail := fs.Float64("fail", 0.05, "share of messages that get an UNDELIV DLR")
		delay := fs.Duration("dlr-delay", 300*time.Millisecond, "delay before the DLR")
		_ = fs.Parse(os.Args[2:])
		v := &sim.Vendor{Mode: sim.VendorMode(*mode), FailRatio: *fail, DLRDelay: *delay, ThrottleN: 10}
		addr, err := v.Start(*listen)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println("vendor simulator listening on", addr)
		t := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fmt.Printf("received %d submits\n", v.Received.Load())
			}
		}
	case "client":
		fs := flag.NewFlagSet("client", flag.ExitOnError)
		addr := fs.String("addr", "127.0.0.1:2775", "gateway SMPP address")
		user := fs.String("user", "", "system id")
		pass := fs.String("pass", "", "password")
		n := fs.Int("n", 1000, "messages to send")
		tps := fs.Int("tps", 100, "messages per second")
		from := fs.String("from", "Telivoz", "sender id")
		prefix := fs.String("prefix", "92300", "destination prefix")
		wait := fs.Duration("wait", 10*time.Second, "how long to wait for DLRs after sending")
		_ = fs.Parse(os.Args[2:])
		c, err := sim.DialClient(ctx, *addr, *user, *pass)
		if err != nil {
			fmt.Println("bind failed:", err)
			os.Exit(1)
		}
		took := c.Blast(ctx, *n, *tps, *from, *prefix)
		fmt.Printf("sent %d in %s (%.1f TPS): %d accepted, %d rejected\n", *n, took.Round(time.Millisecond),
			float64(*n)/took.Seconds(), c.Accepted.Load(), c.Rejected.Load())
		deadline := time.Now().Add(*wait)
		for time.Now().Before(deadline) && c.Delivered.Load()+c.Failed.Load() < c.Accepted.Load() {
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Printf("DLRs: %d delivered, %d failed, %d missing\n", c.Delivered.Load(), c.Failed.Load(),
			c.Accepted.Load()-c.Delivered.Load()-c.Failed.Load())
		c.Session.Close()
	default:
		fmt.Println("usage: smppsim vendor|client [flags]")
		os.Exit(2)
	}
}
