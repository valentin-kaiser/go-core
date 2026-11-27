package mail_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/mail"
)

// smtpReplyMinLen is the length of a status code and its separator.
const smtpReplyMinLen = 4

type received struct {
	from string
	to   []string
	body []byte
}

// startSMTP starts the server on a free port and returns the address and the messages it received
func startSMTP(tb testing.TB, mutate func(*mail.ServerConfig)) (string, func() []received) {
	tb.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Skipf("cannot listen: %v", err)
	}
	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		tb.Skipf("unexpected listener address type %T", l.Addr())
	}
	port := tcpAddr.Port
	err = l.Close()
	if err != nil {
		tb.Skipf("cannot release the listener: %v", err)
	}

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
	err = server.Start(context.Background())
	if err != nil {
		tb.Skipf("cannot start the SMTP server: %v", err)
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Stop(ctx)
		if err != nil {
			tb.Logf("stopping SMTP server failed: %v", err)
		}
	})

	return fmt.Sprintf("127.0.0.1:%d", port), func() []received {
		mu.Lock()
		defer mu.Unlock()
		return append([]received(nil), got...)
	}
}

func waitFor(tb testing.TB, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	tb.Fatal("condition not met in time")
}

func TestSMTPServerReceivesMessages(t *testing.T) {
	addr, messages := startSMTP(t, nil)

	// Lines that start with a dot are dot-stuffed on the wire and must come out unchanged
	body := "Subject: hello\r\n\r\nfirst line\r\n.starts with a dot\r\nlast line\r\n"
	err := smtp.SendMail(addr, nil, "sender@example.com", []string{"a@example.com", "b@example.com"}, []byte(body))
	if err != nil {
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
	defer apperror.Catch(c.Close, "closing connection failed")

	for i := range 5 {
		err := c.Mail("sender@example.com")
		if err != nil {
			t.Fatalf("message %d MAIL: %v", i, err)
		}
		err = c.Rcpt("rcpt@example.com")
		if err != nil {
			t.Fatalf("message %d RCPT: %v", i, err)
		}
		w, err := c.Data()
		if err != nil {
			t.Fatalf("message %d DATA: %v", i, err)
		}
		_, err = fmt.Fprintf(w, "Subject: n%d\r\n\r\nbody %d\r\n", i, i)
		if err != nil {
			t.Fatal(err)
		}
		err = w.Close()
		if err != nil {
			t.Fatalf("message %d end of data: %v", i, err)
		}
	}
	err = c.Quit()
	if err != nil {
		t.Fatal(err)
	}

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
	defer apperror.Catch(c.Close, "closing connection failed")

	err = c.Mail("sender@example.com")
	if err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt("rcpt@example.com")
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 100) + "\r\n"
	for range 1000 { // about 100 KB
		_, err := io.WriteString(w, line)
		if err != nil {
			break
		}
	}
	err = w.Close()
	if err == nil {
		t.Fatal("a message ten times over the limit was accepted")
	}

	// The connection is still in sync: a small message goes through afterwards
	err = c.Reset()
	if err != nil {
		t.Fatalf("RSET after the refused message: %v", err)
	}
	err = c.Mail("sender@example.com")
	if err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt("rcpt@example.com")
	if err != nil {
		t.Fatal(err)
	}
	w, err = c.Data()
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(w, "Subject: small\r\n\r\nok\r\n")
	if err != nil {
		t.Fatal(err)
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("small message after the refused one: %v", err)
	}

	waitFor(t, func() bool { return len(messages()) == 1 })
	if !bytes.Contains(messages()[0].body, []byte("small")) {
		t.Fatalf("the delivered message is %q", messages()[0].body)
	}
}

func benchSMTP(b *testing.B, size int) {
	b.Helper()

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
	defer apperror.Catch(c.Close, "closing connection failed")

	b.SetBytes(int64(body.Len()))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := c.Mail("sender@example.com")
		if err != nil {
			b.Fatal(err)
		}
		err = c.Rcpt("rcpt@example.com")
		if err != nil {
			b.Fatal(err)
		}
		w, err := c.Data()
		if err != nil {
			b.Fatal(err)
		}
		_, err = w.Write(body.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		err = w.Close()
		if err != nil {
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
	defer apperror.Catch(conn.Close, "closing connection failed")
	err = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}

	read := func() string {
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		if err != nil {
			t.Logf("reading reply failed: %v", err)
		}
		return string(buf[:n])
	}
	send := func(s string) string {
		_, err := io.WriteString(conn, s+"\r\n")
		if err != nil {
			t.Fatal(err)
		}
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
	_, err = io.WriteString(conn, "Subject: x\r\n\r\nbody\r\n.\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reply := read()
	if !strings.HasPrefix(reply, "530") {
		t.Fatalf("unauthenticated message got the reply %q, want 530", reply)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(messages()); n != 0 {
		t.Fatalf("%d messages were delivered without authentication", n)
	}
}

// dialSMTP opens a connection and returns a function that sends one line and returns the reply.
func dialSMTP(t *testing.T, addr string) func(line string) string {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("connecting to the test server failed: %v", err)
	}
	t.Cleanup(func() {
		err := conn.Close()
		if err != nil {
			t.Logf("closing connection failed: %v", err)
		}
	})
	err = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		t.Fatalf("setting the deadline failed: %v", err)
	}

	reader := bufio.NewReader(conn)
	// A reply ends with the first line whose fourth character is a space (multi-line replies use a dash).
	read := func() string {
		var reply strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("reading reply failed: %v", err)
			}
			reply.WriteString(line)
			if len(line) < smtpReplyMinLen || line[3] == ' ' {
				return reply.String()
			}
		}
	}
	read() // greeting
	return func(line string) string {
		_, err := io.WriteString(conn, line+"\r\n")
		if err != nil {
			t.Fatalf("sending %q failed: %v", line, err)
		}
		return read()
	}
}

func TestSMTPServerAuthLogin(t *testing.T) {
	const (
		username = "user"
		password = "secret"
	)
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	tests := []struct {
		name     string
		commands []string
		want     string
	}{
		{name: "username after prompt", commands: []string{"AUTH LOGIN", b64(username), b64(password)}, want: "235"},
		{name: "username with command", commands: []string{"AUTH LOGIN " + b64(username), b64(password)}, want: "235"},
		{name: "wrong password", commands: []string{"AUTH LOGIN", b64(username), b64("wrong")}, want: "535"},
		{name: "invalid base64", commands: []string{"AUTH LOGIN", "***"}, want: "501"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, _ := startSMTP(t, func(c *mail.ServerConfig) {
				c.Auth = true
				c.Username = username
				c.Password = password
			})
			send := dialSMTP(t, addr)

			ehlo := send("EHLO client.example")
			if !strings.Contains(ehlo, "AUTH PLAIN LOGIN") {
				t.Fatalf("EHLO reply %q does not offer PLAIN and LOGIN", ehlo)
			}

			var reply string
			for _, command := range tt.commands {
				reply = send(command)
			}
			if !strings.HasPrefix(reply, tt.want) {
				t.Fatalf("last reply %q, want status %s", reply, tt.want)
			}
		})
	}
}

// A server without authentication must not offer it and must take mail without a login.
func TestSMTPServerWithoutAuth(t *testing.T) {
	addr, messages := startSMTP(t, nil)
	send := dialSMTP(t, addr)

	ehlo := send("EHLO client.example")
	if strings.Contains(ehlo, "AUTH") {
		t.Fatalf("EHLO reply %q offers authentication although it is off", ehlo)
	}
	send("MAIL FROM:<a@example.com>")
	send("RCPT TO:<b@example.com>")
	if reply := send("DATA"); !strings.HasPrefix(reply, "354") {
		t.Fatalf("DATA reply %q, want 354", reply)
	}
	reply := send("Subject: x\r\n\r\nbody\r\n.")
	if !strings.HasPrefix(reply, "250") {
		t.Fatalf("message reply %q, want 250", reply)
	}
	if got := len(messages()); got != 1 {
		t.Fatalf("received %d messages, want 1", got)
	}
}
