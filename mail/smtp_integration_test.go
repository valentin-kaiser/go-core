package mail_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/mail"
)

type received struct {
	from string
	to   []string
	body []byte
}

// startSMTP starts the server on a free port and returns the address and the messages it received
func startSMTP(t testing.TB, mutate func(*mail.ServerConfig)) (string, func() []received) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	cfg := mail.ServerConfig{
		Enabled:               true,
		Host:                  "127.0.0.1",
		Port:                  port,
		Domain:                "test.local",
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          5 * time.Second,
		MaxMessageBytes:       1 << 20,
		MaxRecipients:         10,
		AllowInsecureAuth:     true,
		MaxConcurrentHandlers: 8,
	}
	if mutate != nil {
		mutate(&cfg)
	}

	server := mail.NewSMTPServer(cfg, nil)
	var mu sync.Mutex
	var got []received
	server.AddHandler(func(_ context.Context, from string, to []string, r io.Reader) error {
		body, err := io.ReadAll(r)
		mu.Lock()
		got = append(got, received{from: from, to: to, body: body})
		mu.Unlock()
		return err
	})
	if err := server.Start(context.Background()); err != nil {
		t.Skipf("cannot start the SMTP server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	})

	return fmt.Sprintf("127.0.0.1:%d", port), func() []received {
		mu.Lock()
		defer mu.Unlock()
		return append([]received(nil), got...)
	}
}

func waitFor(t testing.TB, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestSMTPServerReceivesMessages(t *testing.T) {
	addr, messages := startSMTP(t, nil)

	// Lines that start with a dot are dot-stuffed on the wire and must come out unchanged
	body := "Subject: hello\r\n\r\nfirst line\r\n.starts with a dot\r\nlast line\r\n"
	if err := smtp.SendMail(addr, nil, "sender@example.com", []string{"a@example.com", "b@example.com"}, []byte(body)); err != nil {
		t.Fatalf("SendMail: %v", err)
	}

	waitFor(t, func() bool { return len(messages()) == 1 })
	m := messages()[0]
	if m.from != "sender@example.com" || len(m.to) != 2 || m.to[1] != "b@example.com" {
		t.Errorf("envelope: from=%q to=%v", m.from, m.to)
	}
	if string(m.body) != body {
		t.Errorf("body differs\ngot:  %q\nwant: %q", m.body, body)
	}
}

func TestSMTPServerSeveralMessagesOnOneConnection(t *testing.T) {
	addr, messages := startSMTP(t, nil)

	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	for i := 0; i < 5; i++ {
		if err := c.Mail("sender@example.com"); err != nil {
			t.Fatalf("message %d MAIL: %v", i, err)
		}
		if err := c.Rcpt("rcpt@example.com"); err != nil {
			t.Fatalf("message %d RCPT: %v", i, err)
		}
		w, err := c.Data()
		if err != nil {
			t.Fatalf("message %d DATA: %v", i, err)
		}
		if _, err := fmt.Fprintf(w, "Subject: n%d\r\n\r\nbody %d\r\n", i, i); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("message %d end of data: %v", i, err)
		}
	}
	_ = c.Quit()

	waitFor(t, func() bool { return len(messages()) == 5 })
}

// MaxMessageBytes is advertised in the EHLO response; a message above it has to be refused
// and must not be held in memory.
func TestSMTPServerRefusesOversizedMessage(t *testing.T) {
	addr, messages := startSMTP(t, func(c *mail.ServerConfig) { c.MaxMessageBytes = 10 * 1024 })

	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Mail("sender@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("rcpt@example.com"); err != nil {
		t.Fatal(err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 100) + "\r\n"
	for i := 0; i < 1000; i++ { // about 100 KB
		if _, err := io.WriteString(w, line); err != nil {
			break
		}
	}
	if err := w.Close(); err == nil {
		t.Fatal("a message ten times over the limit was accepted")
	}

	// The connection is still in sync: a small message goes through afterwards
	if err := c.Reset(); err != nil {
		t.Fatalf("RSET after the refused message: %v", err)
	}
	if err := c.Mail("sender@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("rcpt@example.com"); err != nil {
		t.Fatal(err)
	}
	w, err = c.Data()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "Subject: small\r\n\r\nok\r\n")
	if err := w.Close(); err != nil {
		t.Fatalf("small message after the refused one: %v", err)
	}

	waitFor(t, func() bool { return len(messages()) == 1 })
	if !bytes.Contains(messages()[0].body, []byte("small")) {
		t.Fatalf("the delivered message is %q", messages()[0].body)
	}
}

func benchSMTP(b *testing.B, size int) {
	addr, messages := startSMTP(b, func(c *mail.ServerConfig) { c.MaxMessageBytes = 8 << 20 })

	var body bytes.Buffer
	body.WriteString("Subject: bench\r\n\r\n")
	line := strings.Repeat("x", 76) + "\r\n"
	for body.Len() < size {
		body.WriteString(line)
	}

	c, err := smtp.Dial(addr)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	b.SetBytes(int64(body.Len()))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Mail("sender@example.com"); err != nil {
			b.Fatal(err)
		}
		if err := c.Rcpt("rcpt@example.com"); err != nil {
			b.Fatal(err)
		}
		w, err := c.Data()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write(body.Bytes()); err != nil {
			b.Fatal(err)
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	waitFor(b, func() bool { return len(messages()) >= b.N })
}

// Messages over one SMTP connection to the server, with a handler that reads them
func BenchmarkSMTPServerSmall(b *testing.B)  { benchSMTP(b, 2<<10) }
func BenchmarkSMTPServerMedium(b *testing.B) { benchSMTP(b, 200<<10) }

// A server that requires authentication must still refuse a message from an unauthenticated client.
func TestSMTPServerAuthRequiredForData(t *testing.T) {
	addr, messages := startSMTP(t, func(c *mail.ServerConfig) {
		c.Auth = true
		c.Username = "user"
		c.Password = "secret"
	})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	read := func() string {
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		return string(buf[:n])
	}
	send := func(s string) string {
		_, _ = io.WriteString(conn, s+"\r\n")
		return read()
	}

	read() // greeting
	send("EHLO client.example")
	send("MAIL FROM:<a@example.com>")
	send("RCPT TO:<b@example.com>")
	if reply := send("DATA"); !strings.HasPrefix(reply, "354") {
		// Refused before the data: also fine
		if !strings.HasPrefix(reply, "5") {
			t.Fatalf("DATA reply %q", reply)
		}
		return
	}
	_, _ = io.WriteString(conn, "Subject: x\r\n\r\nbody\r\n.\r\n")
	reply := read()
	if !strings.HasPrefix(reply, "530") {
		t.Fatalf("unauthenticated message got the reply %q, want 530", reply)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(messages()); n != 0 {
		t.Fatalf("%d messages were delivered without authentication", n)
	}
}
