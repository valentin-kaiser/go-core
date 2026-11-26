package email_test

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/mail/internal/email"
)

const (
	fixedDate      = "Mon, 01 Jan 2024 12:00:00 +0000"
	fixedMessageID = "<fixed@example.test>"
	headerBodySep  = "\r\n\r\n"
	textBody       = "plain text body"
	htmlBody       = "<p>html body</p>"
	inlineFile     = "logo.png"
	attachedFile   = "a.txt"
)

var boundaryPattern = regexp.MustCompile(`[0-9a-f]{60}`)

// normalizeMessage replaces the random boundaries by their order of appearance and sorts the header lines,
// which are written in map order.
func normalizeMessage(raw []byte) string {
	text := string(raw)
	seen := map[string]string{}
	text = boundaryPattern.ReplaceAllStringFunc(text, func(boundary string) string {
		if _, ok := seen[boundary]; !ok {
			seen[boundary] = "BOUNDARY" + strconv.Itoa(len(seen)+1)
		}
		return seen[boundary]
	})

	head, body, found := strings.Cut(text, headerBodySep)
	if !found {
		return text
	}

	var fields []string
	for line := range strings.SplitSeq(head, "\r\n") {
		if strings.HasPrefix(line, " ") && len(fields) > 0 {
			fields[len(fields)-1] += "\r\n" + line
			continue
		}
		fields = append(fields, line)
	}
	sort.Strings(fields)
	return strings.Join(fields, "\r\n") + headerBodySep + body
}

func newFixedEmail(text, html string) *email.Email {
	e := email.New()
	e.From = "sender@example.test"
	e.To = []string{"rcpt@example.test"}
	e.Subject = "subject"
	e.Headers.Set("Date", fixedDate)
	e.Headers.Set("Message-Id", fixedMessageID)
	if text != "" {
		e.Text = []byte(text)
	}
	if html != "" {
		e.HTML = []byte(html)
	}
	return e
}

// withAttachment attaches a small text file to the email, reading from memory cannot fail.
func withAttachment(e *email.Email, filename string, htmlRelated bool) *email.Email {
	a, err := e.Attach(strings.NewReader("data of "+filename), filename, "text/plain")
	if err != nil {
		panic(err)
	}
	a.HTMLRelated = htmlRelated
	return e
}

// TestBytesLayouts pins the exact output of every combination of body and attachments.
// The expected values include the nesting of multipart/related that is produced when
// inline attachments are combined with other attachments or with a text alternative.
func TestBytesLayouts(t *testing.T) {
	tests := []struct {
		name  string
		build func() *email.Email
		want  string
	}{
		{
			name:  "text only",
			build: func() *email.Email { return newFixedEmail(textBody, "") },
			want:  "Content-Transfer-Encoding: quoted-printable\r\nContent-Type: text/plain; charset=UTF-8\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\nplain text body",
		},
		{
			name:  "html only",
			build: func() *email.Email { return newFixedEmail("", htmlBody) },
			want:  "Content-Transfer-Encoding: quoted-printable\r\nContent-Type: text/html; charset=UTF-8\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n<p>html body</p>",
		},
		{
			name:  "text and html",
			build: func() *email.Email { return newFixedEmail(textBody, htmlBody) },
			want:  "Content-Type: multipart/alternative;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nplain text body\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<p>html body</p>\r\n--BOUNDARY1--\r\n",
		},
		{
			name: "text and attachment",
			build: func() *email.Email {
				return withAttachment(newFixedEmail(textBody, ""), attachedFile, false)
			},
			want: "Content-Type: multipart/mixed;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nplain text body\r\n--BOUNDARY1\r\nContent-Disposition: attachment;\r\n filename=\"a.txt\"\r\nContent-Id: <a.txt>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBhLnR4dA==\r\n\r\n--BOUNDARY1--\r\n",
		},
		{
			name: "html and inline attachment",
			build: func() *email.Email {
				return withAttachment(newFixedEmail("", htmlBody), inlineFile, true)
			},
			want: "Content-Type: multipart/related;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<p>html body</p>\r\n--BOUNDARY1\r\nContent-Disposition: inline;\r\n filename=\"logo.png\"\r\nContent-Id: <logo.png>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBsb2dvLnBuZw==\r\n\r\n--BOUNDARY1--\r\n",
		},
		{
			name: "text, html and attachment",
			build: func() *email.Email {
				return withAttachment(newFixedEmail(textBody, htmlBody), attachedFile, false)
			},
			want: "Content-Type: multipart/mixed;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Type: multipart/alternative;\r\n boundary=BOUNDARY2\r\n\r\n--BOUNDARY2\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nplain text body\r\n--BOUNDARY2\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<p>html body</p>\r\n--BOUNDARY2--\r\n\r\n--BOUNDARY1\r\nContent-Disposition: attachment;\r\n filename=\"a.txt\"\r\nContent-Id: <a.txt>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBhLnR4dA==\r\n\r\n--BOUNDARY1--\r\n",
		},
		{
			name: "html, inline attachment and attachment",
			build: func() *email.Email {
				e := withAttachment(newFixedEmail("", htmlBody), inlineFile, true)
				return withAttachment(e, attachedFile, false)
			},
			want: "Content-Type: multipart/mixed;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Type: multipart/related;\r\n boundary=BOUNDARY2\r\n\r\n\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<p>html body</p>\r\n--BOUNDARY1\r\nContent-Disposition: inline;\r\n filename=\"logo.png\"\r\nContent-Id: <logo.png>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBsb2dvLnBuZw==\r\n\r\n--BOUNDARY1--\r\n--BOUNDARY1\r\nContent-Disposition: attachment;\r\n filename=\"a.txt\"\r\nContent-Id: <a.txt>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBhLnR4dA==\r\n\r\n--BOUNDARY1--\r\n",
		},
		{
			name: "text, html and inline attachment",
			build: func() *email.Email {
				return withAttachment(newFixedEmail(textBody, htmlBody), inlineFile, true)
			},
			want: "Content-Type: multipart/alternative;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nplain text body\r\n--BOUNDARY1\r\nContent-Type: multipart/related;\r\n boundary=BOUNDARY2\r\n\r\n\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<p>html body</p>\r\n--BOUNDARY1\r\nContent-Disposition: inline;\r\n filename=\"logo.png\"\r\nContent-Id: <logo.png>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBsb2dvLnBuZw==\r\n\r\n--BOUNDARY1--\r\n\r\n--BOUNDARY1--\r\n",
		},
		{
			name: "text, html, inline attachment and attachment",
			build: func() *email.Email {
				e := withAttachment(newFixedEmail(textBody, htmlBody), inlineFile, true)
				return withAttachment(e, attachedFile, false)
			},
			want: "Content-Type: multipart/mixed;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Type: multipart/alternative;\r\n boundary=BOUNDARY2\r\n\r\n--BOUNDARY2\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nplain text body\r\n--BOUNDARY2\r\nContent-Type: multipart/related;\r\n boundary=BOUNDARY3\r\n\r\n\r\n--BOUNDARY1\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n<p>html body</p>\r\n--BOUNDARY1\r\nContent-Disposition: inline;\r\n filename=\"logo.png\"\r\nContent-Id: <logo.png>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBsb2dvLnBuZw==\r\n\r\n--BOUNDARY1--\r\n\r\n--BOUNDARY2--\r\n--BOUNDARY1\r\nContent-Disposition: attachment;\r\n filename=\"a.txt\"\r\nContent-Id: <a.txt>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBhLnR4dA==\r\n\r\n--BOUNDARY1--\r\n",
		},
		{
			name: "attachment only",
			build: func() *email.Email {
				return withAttachment(newFixedEmail("", ""), attachedFile, false)
			},
			want: "Content-Type: multipart/mixed;\r\n boundary=BOUNDARY1\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nFrom: sender@example.test\r\nMessage-Id: <fixed@example.test>\r\nMime-Version: 1.0\r\nSubject: subject\r\nTo: rcpt@example.test\r\n\r\n--BOUNDARY1\r\nContent-Disposition: attachment;\r\n filename=\"a.txt\"\r\nContent-Id: <a.txt>\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nZGF0YSBvZiBhLnR4dA==\r\n\r\n--BOUNDARY1--\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.build().Bytes()
			if err != nil {
				t.Fatalf("Bytes() failed: %v", err)
			}
			if normalized := normalizeMessage(got); normalized != tt.want {
				t.Errorf("Bytes() = %q, want %q", normalized, tt.want)
			}
		})
	}
}

func TestBytesRejectsInlineAttachmentWithoutHTMLBody(t *testing.T) {
	e := withAttachment(newFixedEmail(textBody, ""), inlineFile, true)

	_, err := e.Bytes()
	if err == nil {
		t.Fatal("Bytes() succeeded, want an error for an inline attachment without HTML body")
	}
}

func TestNewFromReaderParsesBytesOutput(t *testing.T) {
	tests := []struct {
		name            string
		build           func() *email.Email
		wantText        string
		wantHTML        string
		wantAttachments []string
	}{
		{
			name:     "text only",
			build:    func() *email.Email { return newFixedEmail(textBody, "") },
			wantText: textBody,
		},
		{
			name:     "text and html",
			build:    func() *email.Email { return newFixedEmail(textBody, htmlBody) },
			wantText: textBody,
			wantHTML: htmlBody,
		},
		{
			name: "text and attachment",
			build: func() *email.Email {
				return withAttachment(newFixedEmail(textBody, ""), attachedFile, false)
			},
			wantText:        textBody,
			wantAttachments: []string{attachedFile},
		},
		{
			name: "text, html and attachment",
			build: func() *email.Email {
				return withAttachment(newFixedEmail(textBody, htmlBody), attachedFile, false)
			},
			wantText:        textBody,
			wantHTML:        htmlBody,
			wantAttachments: []string{attachedFile},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := tt.build().Bytes()
			if err != nil {
				t.Fatalf("Bytes() failed: %v", err)
			}

			parsed, err := email.NewFromReader(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatalf("NewFromReader() failed: %v", err)
			}

			if string(parsed.Text) != tt.wantText {
				t.Errorf("Text = %q, want %q", parsed.Text, tt.wantText)
			}
			if string(parsed.HTML) != tt.wantHTML {
				t.Errorf("HTML = %q, want %q", parsed.HTML, tt.wantHTML)
			}
			var gotAttachments []string
			for _, a := range parsed.Attachments {
				gotAttachments = append(gotAttachments, a.Filename)
			}
			if strings.Join(gotAttachments, ",") != strings.Join(tt.wantAttachments, ",") {
				t.Errorf("Attachments = %v, want %v", gotAttachments, tt.wantAttachments)
			}
			if parsed.Subject != "subject" || parsed.From != "sender@example.test" {
				t.Errorf("Subject, From = %q, %q", parsed.Subject, parsed.From)
			}
		})
	}
}

func TestNewFromReaderDecodesEncodedHeadersAndAddressLists(t *testing.T) {
	const raw = "Subject: =?UTF-8?q?caf=C3=A9?=\r\nFrom: =?UTF-8?q?J=C3=BCrgen?= <j@example.test>\r\nTo: a@example.test, b@example.test\r\nCc: c@example.test\r\nX-Custom: kept\r\nContent-Type: text/plain\r\n\r\nbody"

	parsed, err := email.NewFromReader(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("NewFromReader() failed: %v", err)
	}

	if parsed.Subject != "café" {
		t.Errorf("Subject = %q, want %q", parsed.Subject, "café")
	}
	if parsed.From != "Jürgen <j@example.test>" {
		t.Errorf("From = %q, want %q", parsed.From, "Jürgen <j@example.test>")
	}
	if strings.Join(parsed.To, "|") != "a@example.test|b@example.test" {
		t.Errorf("To = %v", parsed.To)
	}
	if strings.Join(parsed.Cc, "|") != "c@example.test" {
		t.Errorf("Cc = %v", parsed.Cc)
	}
	if parsed.Headers.Get("X-Custom") != "kept" {
		t.Errorf("X-Custom = %q, want %q", parsed.Headers.Get("X-Custom"), "kept")
	}
	if string(parsed.Text) != "body" {
		t.Errorf("Text = %q, want %q", parsed.Text, "body")
	}
}

func TestNewFromReaderHandlesContentDisposition(t *testing.T) {
	const multipartHead = "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\n"
	const multipartTail = "\r\n--b--\r\n"

	tests := []struct {
		name            string
		part            string
		wantAttachments int
		wantText        string
		wantErr         bool
	}{
		{
			name:            "attachment without file name is attached",
			part:            "Content-Disposition: attachment\r\n\r\ndata",
			wantAttachments: 1,
		},
		{
			name:            "inline with file name is attached",
			part:            "Content-Type: text/plain\r\nContent-Disposition: inline; filename=\"x.txt\"\r\n\r\ndata",
			wantAttachments: 1,
		},
		{
			name:     "inline without file name is the body",
			part:     "Content-Type: text/plain\r\nContent-Disposition: inline\r\n\r\ninline text",
			wantText: "inline text",
		},
		{
			name:    "malformed disposition fails",
			part:    "Content-Type: text/plain\r\nContent-Disposition: bogus;;;\r\n\r\ndata",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := email.NewFromReader(strings.NewReader(multipartHead + tt.part + multipartTail))
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewFromReader() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(parsed.Attachments) != tt.wantAttachments {
				t.Errorf("attachments = %d, want %d", len(parsed.Attachments), tt.wantAttachments)
			}
			if string(parsed.Text) != tt.wantText {
				t.Errorf("Text = %q, want %q", parsed.Text, tt.wantText)
			}
		})
	}
}

func TestNewFromReaderDecodesTransferEncodings(t *testing.T) {
	const raw = "Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\nY2Fmw6k=\r\n" +
		"--inner\r\nContent-Type: text/html\r\n\r\n<b>x</b>\r\n--inner--\r\n" +
		"--outer\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"f.bin\"\r\nContent-Transfer-Encoding: base64\r\n\r\nAQID\r\n" +
		"--outer--\r\n"

	parsed, err := email.NewFromReader(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("NewFromReader() failed: %v", err)
	}

	if string(parsed.Text) != "café" {
		t.Errorf("Text = %q, want %q", parsed.Text, "café")
	}
	if string(parsed.HTML) != "<b>x</b>" {
		t.Errorf("HTML = %q, want %q", parsed.HTML, "<b>x</b>")
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Filename != "f.bin" || string(parsed.Attachments[0].Content) != "\x01\x02\x03" {
		t.Errorf("Attachments = %+v", parsed.Attachments)
	}
}

func TestNewFromReaderDecodesQuotedPrintableBody(t *testing.T) {
	const raw = "Content-Type: text/plain\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\ncaf=C3=A9"

	parsed, err := email.NewFromReader(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("NewFromReader() failed: %v", err)
	}
	if string(parsed.Text) != "café" {
		t.Errorf("Text = %q, want %q", parsed.Text, "café")
	}
}

func TestNewFromReaderRejectsMultipartWithoutBoundary(t *testing.T) {
	const raw = "Content-Type: multipart/mixed\r\n\r\nbody"

	_, err := email.NewFromReader(strings.NewReader(raw))
	if err == nil {
		t.Fatal("NewFromReader() succeeded, want an error for a multipart entity without boundary")
	}
}
