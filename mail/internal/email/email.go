// Package email allows to send email messages.
// The package is used internally by the mail package.
package email

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/valentin-kaiser/go-core/apperror"
)

const (
	// MaxLineLength is the maximum line length per RFC 2045
	MaxLineLength = 76
	// DefaultContentType is the default Content-Type according to RFC 2045, section 5.2
	DefaultContentType = "text/plain; charset=us-ascii"
)

var maxBigInt = big.NewInt(math.MaxInt64)

// Email is the type used for email messages
type Email struct {
	ReplyTo     []string
	From        string
	To          []string
	Bcc         []string
	Cc          []string
	Subject     string
	Text        []byte
	HTML        []byte
	Sender      string
	Headers     textproto.MIMEHeader
	Attachments []*Attachment
	ReadReceipt []string
}

// Attachment is a struct representing an email attachment.
// Based on the mime/multipart.FileHeader struct, Attachment contains the name, MIMEHeader, and content of the attachment in question
type Attachment struct {
	Filename    string
	ContentType string
	Header      textproto.MIMEHeader
	Content     []byte
	HTMLRelated bool
}

// part is a copyable representation of a multipart.Part
type part struct {
	header textproto.MIMEHeader
	body   []byte
}

// New creates an Email, and returns the pointer to it.
func New() *Email {
	return &Email{Headers: textproto.MIMEHeader{}}
}

// NewFromReader reads a stream of bytes from an io.Reader, r,
// and returns an email struct containing the parsed data.
// This function expects the data in RFC 5322 format.
func NewFromReader(r io.Reader) (*Email, error) {
	msg := New()
	tp := textproto.NewReader(bufio.NewReader(&trimReader{rd: r}))

	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	for h, v := range headers {
		switch h {
		case "Subject":
			msg.Subject = v[0]
			subj, err := (&mime.WordDecoder{}).DecodeHeader(msg.Subject)
			if err == nil && len(subj) > 0 {
				msg.Subject = subj
			}
			delete(headers, h)
		case "To":
			msg.To = handleAddressList(v)
			delete(headers, h)
		case "Cc":
			msg.Cc = handleAddressList(v)
			delete(headers, h)
		case "Bcc":
			msg.Bcc = handleAddressList(v)
			delete(headers, h)
		case "Reply-To":
			msg.ReplyTo = handleAddressList(v)
			delete(headers, h)
		case "From":
			msg.From = v[0]
			fr, err := (&mime.WordDecoder{}).DecodeHeader(msg.From)
			if err == nil && len(fr) > 0 {
				msg.From = fr
			}
			delete(headers, h)
		}
	}
	msg.Headers = headers
	body := tp.R

	parts, err := parseMIMEParts(msg.Headers, body)
	if err != nil {
		return msg, apperror.Wrap(err)
	}

	for _, p := range parts {
		contentType := p.header.Get("Content-Type")
		if contentType == "" {
			return msg, apperror.NewError("no Content-Type found for MIME entity")
		}

		contentType, _, err = mime.ParseMediaType(p.header.Get("Content-Type"))
		if err != nil {
			return msg, apperror.NewErrorf("could not parse Content-Type header with value %q", p.header.Get("Content-Type")).AddError(err)
		}

		if cd := p.header.Get("Content-Disposition"); cd != "" {
			cd, params, err := mime.ParseMediaType(p.header.Get("Content-Disposition"))
			if err != nil {
				return msg, apperror.NewErrorf("could not parse Content-Disposition header with value %q", p.header.Get("Content-Disposition")).AddError(err)
			}
			filename, filenameDefined := params["filename"]
			if cd == "attachment" || (cd == "inline" && filenameDefined) {
				_, err = msg.Attach(bytes.NewReader(p.body), filename, contentType)
				if err != nil {
					return msg, apperror.Wrap(err)
				}
				continue
			}
		}
		switch contentType {
		case "text/plain":
			msg.Text = p.body
		case "text/html":
			msg.HTML = p.body
		}
	}
	return msg, nil
}

// Attach is used to attach content from an io.Reader to the email.
func (e *Email) Attach(r io.Reader, filename string, contentType string) (*Attachment, error) {
	var buffer bytes.Buffer
	_, err := io.Copy(&buffer, r)
	if err != nil {
		return nil, apperror.NewError("could not read attachment content").AddError(err)
	}
	at := &Attachment{
		Filename:    filename,
		ContentType: contentType,
		Header:      textproto.MIMEHeader{},
		Content:     buffer.Bytes(),
	}
	e.Attachments = append(e.Attachments, at)
	return at, nil
}

// AttachFile is used to attach content to the email.
// It attempts to open the file referenced by filename and, if successful, creates an Attachment.
func (e *Email) AttachFile(filename string) (*Attachment, error) {
	f, err := os.Open(filepath.Clean(filename))
	if err != nil {
		return nil, apperror.NewError("could not open attachment file").AddError(err)
	}
	defer apperror.Catch(f.Close, "could not close attachment file")

	ct := mime.TypeByExtension(filepath.Ext(filename))
	basename := filepath.Base(filename)
	attachment, err := e.Attach(f, basename, ct)
	if err != nil {
		return nil, apperror.Wrap(err)
	}
	return attachment, nil
}

// Bytes converts the Email object to a []byte representation, including all needed MIMEHeaders, boundaries, etc.
func (e *Email) Bytes() ([]byte, error) {
	// Estimate buffer size based on email content
	bufferSize := e.estimateSize()
	buf := bytes.NewBuffer(make([]byte, 0, bufferSize))

	headers, err := e.msgHeaders()
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	htmlAttachments, otherAttachments := e.categorizeAttachments()
	if len(e.HTML) == 0 && len(htmlAttachments) > 0 {
		return nil, apperror.NewError("there are HTML attachments, but no HTML body")
	}

	var (
		isMixed       = len(otherAttachments) > 0
		isAlternative = len(e.Text) > 0 && len(e.HTML) > 0
		isRelated     = len(e.HTML) > 0 && len(htmlAttachments) > 0
	)

	var w *multipart.Writer
	if isMixed || isAlternative || isRelated {
		w = multipart.NewWriter(buf)
	}
	switch {
	case isMixed:
		headers.Set("Content-Type", "multipart/mixed;\r\n boundary="+w.Boundary())
	case isAlternative:
		headers.Set("Content-Type", "multipart/alternative;\r\n boundary="+w.Boundary())
	case isRelated:
		headers.Set("Content-Type", "multipart/related;\r\n boundary="+w.Boundary())
	case len(e.HTML) > 0:
		headers.Set("Content-Type", "text/html; charset=UTF-8")
		headers.Set("Content-Transfer-Encoding", "quoted-printable")
	default:
		headers.Set("Content-Type", "text/plain; charset=UTF-8")
		headers.Set("Content-Transfer-Encoding", "quoted-printable")
	}

	err = headerToBytes(buf, headers)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	_, err = io.WriteString(buf, "\r\n")
	if err != nil {
		return nil, apperror.NewError("could not write headers").AddError(err)
	}

	if len(e.Text) > 0 || len(e.HTML) > 0 {
		var subWriter *multipart.Writer

		subWriter = w
		if isMixed && isAlternative {
			subWriter = multipart.NewWriter(buf)
			header := textproto.MIMEHeader{
				"Content-Type": {"multipart/alternative;\r\n boundary=" + subWriter.Boundary()},
			}
			_, err := w.CreatePart(header)
			if err != nil {
				return nil, apperror.NewError("could not create multipart/alternative part").AddError(err)
			}
		}

		if len(e.Text) > 0 {
			err := writeMessage(buf, e.Text, isMixed || isAlternative, "text/plain", subWriter)
			if err != nil {
				return nil, apperror.Wrap(err)
			}
		}

		if len(e.HTML) > 0 {
			messageWriter := subWriter
			var relatedWriter *multipart.Writer
			if (isMixed || isAlternative) && len(htmlAttachments) > 0 {
				relatedWriter = multipart.NewWriter(buf)
				header := textproto.MIMEHeader{
					"Content-Type": {"multipart/related;\r\n boundary=" + relatedWriter.Boundary()},
				}
				_, err := subWriter.CreatePart(header)
				if err != nil {
					return nil, apperror.NewError("could not create multipart/related part").AddError(err)
				}

				messageWriter = relatedWriter
			}
			if isRelated && len(htmlAttachments) > 0 {
				relatedWriter = w
				messageWriter = w
			}

			err := writeMessage(buf, e.HTML, isMixed || isAlternative || isRelated, "text/html", messageWriter)
			if err != nil {
				return nil, apperror.Wrap(err)
			}

			if len(htmlAttachments) > 0 {
				for _, a := range htmlAttachments {
					a.setDefaultHeaders()
					ap, err := relatedWriter.CreatePart(a.Header)
					if err != nil {
						return nil, apperror.NewError("could not create HTML attachment part").AddError(err)
					}
					err = base64Wrap(ap, a.Content)
					if err != nil {
						return nil, apperror.Wrap(err)
					}
				}

				if isMixed || isAlternative {
					err = relatedWriter.Close()
					if err != nil {
						return nil, apperror.NewError("could not close multipart/related part").AddError(err)
					}
				}
			}
		}
		if isMixed && isAlternative {
			err := subWriter.Close()
			if err != nil {
				return nil, apperror.NewError("could not close multipart/alternative part").AddError(err)
			}
		}
	}

	for _, a := range otherAttachments {
		a.setDefaultHeaders()
		ap, err := w.CreatePart(a.Header)
		if err != nil {
			return nil, apperror.NewError("could not create attachment part").AddError(err)
		}
		err = base64Wrap(ap, a.Content)
		if err != nil {
			return nil, apperror.Wrap(err)
		}
	}
	if isMixed || isAlternative || isRelated {
		err := w.Close()
		if err != nil {
			return nil, apperror.NewError("could not close multipart/writer").AddError(err)
		}
	}
	return buf.Bytes(), nil
}

// Send an email using the given host and SMTP auth (optional), returns any error thrown by smtp.SendMail
// This function merges the To, Cc, and Bcc fields and calls the smtp.SendMail function using the Email.Bytes() output as the message
func (e *Email) Send(address string, auth smtp.Auth, helo string) error {
	to := make([]string, 0, len(e.To)+len(e.Cc)+len(e.Bcc))
	to = append(append(append(to, e.To...), e.Cc...), e.Bcc...)
	for i := 0; i < len(to); i++ {
		addr, err := mail.ParseAddress(to[i])
		if err != nil {
			return err
		}
		to[i] = addr.Address
	}
	if len(to) == 0 {
		return apperror.NewError("at least one To address must be specified")
	}
	sender, err := e.parseSender()
	if err != nil {
		return apperror.Wrap(err)
	}
	raw, err := e.Bytes()
	if err != nil {
		return apperror.Wrap(err)
	}

	if helo == "" {
		err = smtp.SendMail(address, auth, sender, to, raw)
		if err != nil {
			return apperror.Wrap(err)
		}
		return nil
	}

	// Use custom HELO with lower-level SMTP client
	conn, err := smtp.Dial(address)
	if err != nil {
		return apperror.NewError("could not dial SMTP connection").AddError(err)
	}

	// Send custom HELO
	err = conn.Hello(helo)
	if err != nil {
		return apperror.NewError("could not send HELO command").AddError(err)
	}

	if auth != nil {
		err = conn.Auth(auth)
		if err != nil {
			return apperror.NewError("could not authenticate SMTP client").AddError(err)
		}
	}

	err = conn.Mail(sender)
	if err != nil {
		return apperror.NewError("could not set SMTP sender").AddError(err)
	}

	for _, addr := range to {
		err = conn.Rcpt(addr)
		if err != nil {
			return apperror.NewError("could not add SMTP recipient").AddError(err)
		}
	}

	w, err := conn.Data()
	if err != nil {
		return apperror.NewError("could not create SMTP data writer").AddError(err)
	}

	_, err = w.Write(raw)
	if err != nil {
		return apperror.NewError("could not write SMTP data").AddError(err)
	}

	err = w.Close()
	if err != nil {
		return apperror.NewError("could not close SMTP data writer").AddError(err)
	}

	err = conn.Quit()
	if err != nil {
		return apperror.NewError("could not quit SMTP session").AddError(err)
	}

	return nil
}

// SendWithTLS sends an email over tls with an optional TLS config.
func (e *Email) SendWithTLS(address string, auth smtp.Auth, config *tls.Config, helo string) error {
	to := make([]string, 0, len(e.To)+len(e.Cc)+len(e.Bcc))
	to = append(append(append(to, e.To...), e.Cc...), e.Bcc...)
	for i := 0; i < len(to); i++ {
		addr, err := mail.ParseAddress(to[i])
		if err != nil {
			return apperror.NewError("could not parse To address").AddError(err)
		}
		to[i] = addr.Address
	}
	if len(to) == 0 {
		return apperror.NewError("at least one To address must be specified")
	}
	sender, err := e.parseSender()
	if err != nil {
		return apperror.Wrap(err)
	}
	raw, err := e.Bytes()
	if err != nil {
		return apperror.Wrap(err)
	}

	conn, err := tls.Dial("tcp", address, config)
	if err != nil {
		return apperror.NewError("could not dial TLS connection").AddError(err)
	}
	defer apperror.Catch(conn.Close, "could not close TLS connection")
	c, err := smtp.NewClient(conn, strings.Split(address, ":")[0])
	if err != nil {
		return apperror.NewError("could not create SMTP client").AddError(err)
	}
	defer apperror.Catch(c.Quit, "could not quit SMTP session")

	// Send custom HELO if provided (after connection but before auth)
	if helo != "" {
		err = c.Hello(helo)
		if err != nil {
			return apperror.NewError("could not send HELO command").AddError(err)
		}
	}

	if auth != nil {
		err = c.Auth(auth)
		if err != nil {
			return apperror.NewError("could not authenticate SMTP client").AddError(err)
		}
	}
	err = c.Mail(sender)
	if err != nil {
		return apperror.NewError("could not set SMTP sender").AddError(err)
	}
	for _, addr := range to {
		err = c.Rcpt(addr)
		if err != nil {
			return apperror.NewError("could not add SMTP recipient").AddError(err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return apperror.NewError("could not create SMTP data writer").AddError(err)
	}
	_, err = w.Write(raw)
	if err != nil {
		return apperror.NewError("could not write SMTP data").AddError(err)
	}
	err = w.Close()
	if err != nil {
		return apperror.NewError("could not close SMTP data writer").AddError(err)
	}
	err = c.Quit()
	if err != nil {
		return apperror.NewError("could not quit SMTP session").AddError(err)
	}
	return nil
}

// SendWithStartTLS sends an email over TLS using STARTTLS with an optional TLS config.
func (e *Email) SendWithStartTLS(address string, auth smtp.Auth, config *tls.Config, helo string) error {
	to := make([]string, 0, len(e.To)+len(e.Cc)+len(e.Bcc))
	to = append(append(append(to, e.To...), e.Cc...), e.Bcc...)
	for i := 0; i < len(to); i++ {
		addr, err := mail.ParseAddress(to[i])
		if err != nil {
			return apperror.NewError("could not parse To address").AddError(err)
		}
		to[i] = addr.Address
	}
	if len(to) == 0 {
		return apperror.NewError("at least one To address must be specified")
	}
	sender, err := e.parseSender()
	if err != nil {
		return apperror.Wrap(err)
	}
	raw, err := e.Bytes()
	if err != nil {
		return apperror.Wrap(err)
	}

	conn, err := smtp.Dial(address)
	if err != nil {
		return apperror.NewError("could not dial SMTP connection").AddError(err)
	}

	// Send custom HELO if provided (before STARTTLS)
	if helo != "" {
		err = conn.Hello(helo)
		if err != nil {
			return apperror.NewError("could not send HELO command").AddError(err)
		}
	}

	err = conn.StartTLS(config)
	if err != nil {
		return apperror.NewError("could not start TLS").AddError(err)
	}
	if auth != nil {
		err = conn.Auth(auth)
		if err != nil {
			return apperror.NewError("could not authenticate SMTP client").AddError(err)
		}
	}
	err = conn.Mail(sender)
	if err != nil {
		return apperror.NewError("could not set SMTP sender").AddError(err)
	}
	for _, addr := range to {
		err = conn.Rcpt(addr)
		if err != nil {
			return apperror.NewError("could not add SMTP recipient").AddError(err)
		}
	}
	w, err := conn.Data()
	if err != nil {
		return apperror.NewError("could not create SMTP data writer").AddError(err)
	}
	_, err = w.Write(raw)
	if err != nil {
		return apperror.NewError("could not write SMTP data").AddError(err)
	}
	err = w.Close()
	if err != nil {
		return apperror.NewError("could not close SMTP data writer").AddError(err)
	}
	err = conn.Quit()
	if err != nil {
		return apperror.NewError("could not quit SMTP session").AddError(err)
	}
	return nil
}

// msgHeaders merges the Email's various fields and custom headers together in a
// standards compliant way to create a MIMEHeader to be used in the resulting
// message. It does not alter e.Headers.
//
// "e"'s fields To, Cc, From, Subject will be used unless they are present in
// e.Headers. Unless set in e.Headers, "Date" will filled with the current time.
func (e *Email) msgHeaders() (textproto.MIMEHeader, error) {
	res := make(textproto.MIMEHeader, len(e.Headers)+6)
	if e.Headers != nil {
		for _, h := range []string{"Reply-To", "To", "Cc", "From", "Subject", "Date", "Message-Id", "MIME-Version"} {
			if v, ok := e.Headers[h]; ok {
				res[h] = v
			}
		}
	}
	if _, ok := res["Reply-To"]; !ok && len(e.ReplyTo) > 0 {
		res.Set("Reply-To", strings.Join(e.ReplyTo, ", "))
	}
	if _, ok := res["To"]; !ok && len(e.To) > 0 {
		res.Set("To", strings.Join(e.To, ", "))
	}
	if _, ok := res["Cc"]; !ok && len(e.Cc) > 0 {
		res.Set("Cc", strings.Join(e.Cc, ", "))
	}
	if _, ok := res["Subject"]; !ok && e.Subject != "" {
		res.Set("Subject", e.Subject)
	}
	if _, ok := res["Message-Id"]; !ok {
		id, err := generateMessageID()
		if err != nil {
			return nil, apperror.Wrap(err)
		}
		res.Set("Message-Id", id)
	}
	if _, ok := res["From"]; !ok {
		res.Set("From", e.From)
	}
	if _, ok := res["Date"]; !ok {
		res.Set("Date", time.Now().Format(time.RFC1123Z))
	}
	if _, ok := res["MIME-Version"]; !ok {
		res.Set("MIME-Version", "1.0")
	}
	for field, vals := range e.Headers {
		if _, ok := res[field]; !ok {
			res[field] = vals
		}
	}
	return res, nil
}

func (e *Email) categorizeAttachments() (html []*Attachment, other []*Attachment) {
	for _, a := range e.Attachments {
		if a.HTMLRelated {
			html = append(html, a)
			continue
		}

		other = append(other, a)
	}
	return
}

// Select and parse an SMTP envelope sender address.  Choose Email.Sender if set, or fallback to Email.From.
func (e *Email) parseSender() (string, error) {
	if e.Sender != "" {
		sender, err := mail.ParseAddress(e.Sender)
		if err != nil {
			return "", apperror.NewError("could not parse sender address").AddError(err)
		}
		return sender.Address, nil
	}

	if e.From == "" {
		return "", nil
	}

	from, err := mail.ParseAddress(e.From)
	if err != nil {
		return "", apperror.NewError("could not parse From address").AddError(err)
	}
	return from.Address, nil
}

// estimateSize estimates the buffer size needed for the email serialization
func (e *Email) estimateSize() int {
	const (
		headerOverhead = 1024 // Estimated overhead for headers and MIME boundaries
		base64Overhead = 1.34 // Base64 encoding increases size by ~33%
		minBufferSize  = 1024 // Minimum buffer size for small emails
	)

	size := headerOverhead
	size += len(e.Text)
	size += len(e.HTML)
	for _, attachment := range e.Attachments {
		size += int(float64(len(attachment.Content)) * base64Overhead)
	}

	if size < minBufferSize {
		size = minBufferSize
	}

	return size
}

func (a *Attachment) setDefaultHeaders() {
	contentType := "application/octet-stream"
	if len(a.ContentType) > 0 {
		contentType = a.ContentType
	}
	a.Header.Set("Content-Type", contentType)

	if len(a.Header.Get("Content-Disposition")) == 0 {
		disposition := "attachment"
		if a.HTMLRelated {
			disposition = "inline"
		}
		a.Header.Set("Content-Disposition", fmt.Sprintf("%s;\r\n filename=\"%s\"", disposition, a.Filename))
	}
	if len(a.Header.Get("Content-ID")) == 0 {
		a.Header.Set("Content-ID", fmt.Sprintf("<%s>", a.Filename))
	}
	if len(a.Header.Get("Content-Transfer-Encoding")) == 0 {
		a.Header.Set("Content-Transfer-Encoding", "base64")
	}
}

func writeMessage(buf io.Writer, msg []byte, multipart bool, mediaType string, w *multipart.Writer) error {
	if multipart {
		header := textproto.MIMEHeader{
			"Content-Type":              {mediaType + "; charset=UTF-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		}
		_, err := w.CreatePart(header)
		if err != nil {
			return apperror.Wrap(err)
		}
	}

	qp := quotedprintable.NewWriter(buf)
	_, err := qp.Write(msg)
	if err != nil {
		return apperror.NewError("could not write message").AddError(err)
	}
	err = qp.Close()
	if err != nil {
		return apperror.NewError("could not close quoted-printable writer").AddError(err)
	}
	return nil
}

// parseMIMEParts will recursively walk a MIME entity and return a []mime.Part containing
// each (flattened) mime.Part found.
func parseMIMEParts(hs textproto.MIMEHeader, b io.Reader) ([]*part, error) {
	var ps []*part
	if _, ok := hs["Content-Type"]; !ok {
		hs.Set("Content-Type", DefaultContentType)
	}
	ct, params, err := mime.ParseMediaType(hs.Get("Content-Type"))
	if err != nil {
		return ps, apperror.NewErrorf("could not parse Content-Type header with value %q", hs.Get("Content-Type")).AddError(err)
	}
	if strings.HasPrefix(ct, "multipart/") {
		if _, ok := params["boundary"]; !ok {
			return ps, apperror.NewError("no boundary found for multipart entity")
		}
		mr := multipart.NewReader(b, params["boundary"])
		for {
			var buf bytes.Buffer
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return ps, apperror.NewError("could not read multipart part").AddError(err)
			}
			if _, ok := p.Header["Content-Type"]; !ok {
				p.Header.Set("Content-Type", DefaultContentType)
			}
			subct, _, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if err != nil {
				return ps, apperror.NewErrorf("could not parse Content-Type header with value %q", p.Header.Get("Content-Type")).AddError(err)
			}
			if strings.HasPrefix(subct, "multipart/") {
				sps, err := parseMIMEParts(p.Header, p)
				if err != nil {
					return ps, apperror.NewError("could not parse multipart parts").AddError(err)
				}
				ps = append(ps, sps...)
				continue
			}

			var reader io.Reader
			reader = p
			const cte = "Content-Transfer-Encoding"
			if p.Header.Get(cte) == "base64" {
				reader = base64.NewDecoder(base64.StdEncoding, reader)
			}
			_, err = io.Copy(&buf, reader)
			if err != nil {
				return ps, apperror.NewError("could not copy part data").AddError(err)
			}
			ps = append(ps, &part{body: buf.Bytes(), header: p.Header})
		}
		return ps, nil
	}

	switch hs.Get("Content-Transfer-Encoding") {
	case "quoted-printable":
		b = quotedprintable.NewReader(b)
	case "base64":
		b = base64.NewDecoder(base64.StdEncoding, b)
	}
	var buf bytes.Buffer
	_, err = io.Copy(&buf, b)
	if err != nil {
		return ps, apperror.NewError("could not copy part data").AddError(err)
	}
	ps = append(ps, &part{body: buf.Bytes(), header: hs})

	return ps, nil
}

// base64Wrap encodes the attachment content, and wraps it according to RFC 2045 standards (every 76 chars)
// The output is then written to the specified io.Writer
func base64Wrap(w io.Writer, b []byte) error {
	// 57 raw bytes per 76-byte base64 line.
	const maxRaw = 57
	// Buffer for each line, including trailing CRLF.
	buf := make([]byte, MaxLineLength+len("\r\n"))
	copy(buf[MaxLineLength:], "\r\n")
	// Process raw chunks until there's no longer enough to fill a line.
	for len(b) >= maxRaw {
		base64.StdEncoding.Encode(buf, b[:maxRaw])
		_, err := w.Write(buf)
		if err != nil {
			return apperror.NewError("could not write base64 wrapped data").AddError(err)
		}
		b = b[maxRaw:]
	}
	// Handle the last chunk of bytes.
	if len(b) > 0 {
		out := buf[:base64.StdEncoding.EncodedLen(len(b))]
		base64.StdEncoding.Encode(out, b)
		out = append(out, "\r\n"...)
		_, err := w.Write(out)
		if err != nil {
			return apperror.NewError("could not write base64 wrapped data").AddError(err)
		}
	}

	return nil
}

// headerToBytes renders "header" to "buff". If there are multiple values for a
// field, multiple "Field: value\r\n" lines will be emitted.
func headerToBytes(buff io.Writer, header textproto.MIMEHeader) error {
	for field, vals := range header {
		for _, subval := range vals {
			_, err := io.WriteString(buff, field)
			if err != nil {
				return apperror.NewError("could not write header field").AddError(err)
			}
			_, err = io.WriteString(buff, ": ")
			if err != nil {
				return apperror.NewError("could not write header field separator").AddError(err)
			}
			switch field {
			case "Content-Type", "Content-Disposition":
				_, err := buff.Write([]byte(subval))
				if err != nil {
					return apperror.NewError("could not write header field value").AddError(err)
				}
			case "From", "To", "Cc", "Bcc":
				_, err := buff.Write([]byte(subval))
				if err != nil {
					return apperror.NewError("could not write header field value").AddError(err)
				}
			default:
				_, err := buff.Write([]byte(mime.QEncoding.Encode("UTF-8", subval)))
				if err != nil {
					return apperror.NewError("could not write header field value").AddError(err)
				}
			}
			_, err = io.WriteString(buff, "\r\n")
			if err != nil {
				return apperror.NewError("could not write header field terminator").AddError(err)
			}
		}
	}

	return nil
}

func generateMessageID() (string, error) {
	t := time.Now().UnixNano()
	pid := os.Getpid()
	rint, err := rand.Int(rand.Reader, maxBigInt)
	if err != nil {
		return "", apperror.NewError("could not generate random integer").AddError(err)
	}
	h, err := os.Hostname()
	if err != nil {
		h = "localhost.localdomain"
	}
	msgid := fmt.Sprintf("<%d.%d.%d@%s>", t, pid, rint, h)
	return msgid, nil
}

func handleAddressList(v []string) []string {
	res := []string{}
	for _, a := range v {
		w := strings.Split(a, ",")
		for _, addr := range w {
			decodedAddr, err := (&mime.WordDecoder{}).DecodeHeader(strings.TrimSpace(addr))
			if err == nil {
				res = append(res, decodedAddr)
				continue
			}

			res = append(res, addr)
		}
	}
	return res
}

// trimReader is a custom io.Reader that will trim any leading
// whitespace, as this can cause email imports to fail.
type trimReader struct {
	rd      io.Reader
	trimmed bool
}

// Read trims off any unicode whitespace from the originating reader
func (tr *trimReader) Read(buf []byte) (int, error) {
	n, err := tr.rd.Read(buf)
	if err != nil {
		return n, err
	}
	if !tr.trimmed {
		t := bytes.TrimLeftFunc(buf[:n], unicode.IsSpace)
		tr.trimmed = true
		n = copy(buf, t)
	}
	return n, nil
}
