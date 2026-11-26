// Package email allows to send email messages.
// The package is used internally by the mail package.
package email

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/valentin-kaiser/go-core/v2/apperror"
)

// Header names, media types and encodings used while building and parsing messages.
const (
	headerContentType             = "Content-Type"
	headerContentTransferEncoding = "Content-Transfer-Encoding"
	headerContentDisposition      = "Content-Disposition"
	headerContentID               = "Content-ID"
	headerTo                      = "To"
	headerFrom                    = "From"
	headerCc                      = "Cc"
	headerBcc                     = "Bcc"
	headerSubject                 = "Subject"
	headerReplyTo                 = "Reply-To"
	headerMessageID               = "Message-Id"
	headerMIMEVersion             = "MIME-Version"
	headerDate                    = "Date"

	mimeVersion = "1.0"

	encodingQuotedPrintable = "quoted-printable"
	encodingBase64          = "base64"

	mediaTypeTextPlain       = "text/plain"
	mediaTypeTextHTML        = "text/html"
	mediaTypeOctetStream     = "application/octet-stream"
	mediaTypeMultipartPrefix = "multipart/"

	multipartMixedBoundary       = "multipart/mixed;\r\n boundary="
	multipartAlternativeBoundary = "multipart/alternative;\r\n boundary="
	multipartRelatedBoundary     = "multipart/related;\r\n boundary="

	charsetUTF8       = "UTF-8"
	charsetUTF8Suffix = "; charset=" + charsetUTF8
	textHTMLUTF8      = mediaTypeTextHTML + charsetUTF8Suffix
	textPlainUTF8     = mediaTypeTextPlain + charsetUTF8Suffix

	dispositionInline     = "inline"
	dispositionAttachment = "attachment"

	paramBoundary = "boundary"
	paramFilename = "filename"

	networkTCP       = "tcp"
	fallbackHostname = "localhost.localdomain"
)

const (
	// MaxLineLength is the maximum line length per RFC 2045
	MaxLineLength = 76
	// DefaultContentType is the default Content-Type according to RFC 2045, section 5.2
	DefaultContentType = "text/plain; charset=us-ascii"
)

var maxBigInt = big.NewInt(math.MaxInt64)

// Attachment is a struct representing an email attachment.
// Based on the mime/multipart.FileHeader struct, Attachment contains the name, MIMEHeader, and content of the attachment in question
type Attachment struct {
	Filename    string
	ContentType string
	Header      textproto.MIMEHeader
	Content     []byte
	HTMLRelated bool
}

func (a *Attachment) setDefaultHeaders() {
	contentType := mediaTypeOctetStream
	if len(a.ContentType) > 0 {
		contentType = a.ContentType
	}
	a.Header.Set(headerContentType, contentType)

	if len(a.Header.Get(headerContentDisposition)) == 0 {
		disposition := dispositionAttachment
		if a.HTMLRelated {
			disposition = dispositionInline
		}
		a.Header.Set(headerContentDisposition, fmt.Sprintf("%s;\r\n filename=\"%s\"", disposition, a.Filename))
	}
	if len(a.Header.Get(headerContentID)) == 0 {
		a.Header.Set(headerContentID, fmt.Sprintf("<%s>", a.Filename))
	}
	if len(a.Header.Get(headerContentTransferEncoding)) == 0 {
		a.Header.Set(headerContentTransferEncoding, encodingBase64)
	}
}

// part is a copyable representation of a multipart.Part
type part struct {
	header textproto.MIMEHeader
	body   []byte
}

// multipartLayout tells which multipart containers an email needs.
type multipartLayout struct {
	mixed       bool
	alternative bool
	related     bool
}

func (l multipartLayout) isMultipart() bool {
	return l.mixed || l.alternative || l.related
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
		return nil, apperror.NewError("reading MIME headers failed").AddError(err)
	}

	for h, v := range headers {
		switch h {
		case headerSubject:
			msg.Subject = v[0]
			subj, err := (&mime.WordDecoder{}).DecodeHeader(msg.Subject)
			if err == nil && len(subj) > 0 {
				msg.Subject = subj
			}
			delete(headers, h)
		case headerTo:
			msg.To = handleAddressList(v)
			delete(headers, h)
		case headerCc:
			msg.Cc = handleAddressList(v)
			delete(headers, h)
		case headerBcc:
			msg.Bcc = handleAddressList(v)
			delete(headers, h)
		case headerReplyTo:
			msg.ReplyTo = handleAddressList(v)
			delete(headers, h)
		case headerFrom:
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
		return msg, apperror.NewError("parsing MIME parts failed").AddError(err)
	}

	for _, p := range parts {
		err = msg.addPart(p)
		if err != nil {
			return msg, err
		}
	}
	return msg, nil
}

// Attach is used to attach content from an io.Reader to the email.
func (e *Email) Attach(r io.Reader, filename string, contentType string) (*Attachment, error) {
	var buffer bytes.Buffer
	_, err := io.Copy(&buffer, r)
	if err != nil {
		return nil, apperror.NewError("reading attachment content failed").AddError(err)
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
		return nil, apperror.NewError("opening attachment file failed").AddError(err)
	}
	defer apperror.Catch(f.Close, "closing attachment file failed")

	ct := mime.TypeByExtension(filepath.Ext(filename))
	basename := filepath.Base(filename)
	attachment, err := e.Attach(f, basename, ct)
	if err != nil {
		return nil, apperror.NewErrorf("attaching file %s failed", basename).AddError(err)
	}
	return attachment, nil
}

// Bytes converts the Email object to a []byte representation, including all needed MIMEHeaders, boundaries, etc.
func (e *Email) Bytes() ([]byte, error) {
	bufferSize := e.estimateSize()
	buf := bytes.NewBuffer(make([]byte, 0, bufferSize))

	headers, err := e.msgHeaders()
	if err != nil {
		return nil, apperror.NewError("building message headers failed").AddError(err)
	}

	htmlAttachments, otherAttachments := e.categorizeAttachments()
	if len(e.HTML) == 0 && len(htmlAttachments) > 0 {
		return nil, apperror.NewError("there are HTML attachments, but no HTML body")
	}

	layout := multipartLayout{
		mixed:       len(otherAttachments) > 0,
		alternative: len(e.Text) > 0 && len(e.HTML) > 0,
		related:     len(e.HTML) > 0 && len(htmlAttachments) > 0,
	}

	var w *multipart.Writer
	if layout.isMultipart() {
		w = multipart.NewWriter(buf)
	}
	e.setContentHeaders(headers, layout, w)

	err = headerToBytes(buf, headers)
	if err != nil {
		return nil, apperror.NewError("converting headers to bytes failed").AddError(err)
	}

	_, err = io.WriteString(buf, "\r\n")
	if err != nil {
		return nil, apperror.NewError("writing headers failed").AddError(err)
	}

	if len(e.Text) > 0 || len(e.HTML) > 0 {
		err = e.writeBody(buf, w, layout, htmlAttachments)
		if err != nil {
			return nil, err
		}
	}

	err = writeAttachmentParts(w, otherAttachments, "attachment")
	if err != nil {
		return nil, err
	}
	if layout.isMultipart() {
		err = w.Close()
		if err != nil {
			return nil, apperror.NewError("closing multipart/writer failed").AddError(err)
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
			return apperror.NewErrorf("parsing To address %q failed", to[i]).AddError(err)
		}
		to[i] = addr.Address
	}
	if len(to) == 0 {
		return apperror.NewError("at least one To address must be specified")
	}
	sender, err := e.parseSender()
	if err != nil {
		return apperror.NewError("parsing sender failed").AddError(err)
	}
	raw, err := e.Bytes()
	if err != nil {
		return apperror.NewError("building message bytes failed").AddError(err)
	}

	if helo == "" {
		err = smtp.SendMail(address, auth, sender, to, raw)
		if err != nil {
			return apperror.NewError("sending mail failed").AddError(err)
		}
		return nil
	}

	conn, err := smtp.Dial(address)
	if err != nil {
		return apperror.NewError("dialing SMTP connection failed").AddError(err)
	}

	err = conn.Hello(helo)
	if err != nil {
		return apperror.NewError("sending HELO command failed").AddError(err)
	}

	if auth != nil {
		err = conn.Auth(auth)
		if err != nil {
			return apperror.NewError("authenticating SMTP client failed").AddError(err)
		}
	}

	err = conn.Mail(sender)
	if err != nil {
		return apperror.NewError("setting SMTP sender failed").AddError(err)
	}

	for _, addr := range to {
		err = conn.Rcpt(addr)
		if err != nil {
			return apperror.NewError("adding SMTP recipient failed").AddError(err)
		}
	}

	w, err := conn.Data()
	if err != nil {
		return apperror.NewError("creating SMTP data writer failed").AddError(err)
	}

	_, err = w.Write(raw)
	if err != nil {
		return apperror.NewError("writing SMTP data failed").AddError(err)
	}

	err = w.Close()
	if err != nil {
		return apperror.NewError("closing SMTP data writer failed").AddError(err)
	}

	err = conn.Quit()
	if err != nil {
		return apperror.NewError("quitting SMTP session failed").AddError(err)
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
			return apperror.NewError("parsing To address failed").AddError(err)
		}
		to[i] = addr.Address
	}
	if len(to) == 0 {
		return apperror.NewError("at least one To address must be specified")
	}
	sender, err := e.parseSender()
	if err != nil {
		return apperror.NewError("parsing sender failed").AddError(err)
	}
	raw, err := e.Bytes()
	if err != nil {
		return apperror.NewError("building message bytes failed").AddError(err)
	}

	conn, err := tls.Dial(networkTCP, address, config)
	if err != nil {
		return apperror.NewError("dialing TLS connection failed").AddError(err)
	}
	c, err := smtp.NewClient(conn, strings.Split(address, ":")[0])
	if err != nil {
		// smtp.NewClient did not take ownership of conn on failure.
		apperror.Catch(conn.Close, "closing connection failed")
		return apperror.NewError("creating SMTP client failed").AddError(err)
	}
	// Quit closes the underlying connection. Close is retained only as a
	// best-effort fallback for early returns; its error must not hide the
	// operation error or panic when Quit has already closed the connection.
	defer apperror.Catch(func() error {
		err := c.Close()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}, "closing SMTP client failed")

	// Send custom HELO if provided (after connection but before auth)
	if helo != "" {
		err = c.Hello(helo)
		if err != nil {
			return apperror.NewError("sending HELO command failed").AddError(err)
		}
	}

	if auth != nil {
		err = c.Auth(auth)
		if err != nil {
			return apperror.NewError("authenticating SMTP client failed").AddError(err)
		}
	}
	err = c.Mail(sender)
	if err != nil {
		return apperror.NewError("setting SMTP sender failed").AddError(err)
	}
	for _, addr := range to {
		err = c.Rcpt(addr)
		if err != nil {
			return apperror.NewError("adding SMTP recipient failed").AddError(err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return apperror.NewError("creating SMTP data writer failed").AddError(err)
	}
	_, err = w.Write(raw)
	if err != nil {
		return apperror.NewError("writing SMTP data failed").AddError(err)
	}
	err = w.Close()
	if err != nil {
		return apperror.NewError("closing SMTP data writer failed").AddError(err)
	}
	err = c.Quit()
	if err != nil {
		return apperror.NewError("quitting SMTP session failed").AddError(err)
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
			return apperror.NewError("parsing To address failed").AddError(err)
		}
		to[i] = addr.Address
	}
	if len(to) == 0 {
		return apperror.NewError("at least one To address must be specified")
	}
	sender, err := e.parseSender()
	if err != nil {
		return apperror.NewError("parsing sender failed").AddError(err)
	}
	raw, err := e.Bytes()
	if err != nil {
		return apperror.NewError("building message bytes failed").AddError(err)
	}

	conn, err := smtp.Dial(address)
	if err != nil {
		return apperror.NewError("dialing SMTP connection failed").AddError(err)
	}

	// Send custom HELO if provided (before STARTTLS)
	if helo != "" {
		err = conn.Hello(helo)
		if err != nil {
			return apperror.NewError("sending HELO command failed").AddError(err)
		}
	}

	err = conn.StartTLS(config)
	if err != nil {
		return apperror.NewError("starting TLS failed").AddError(err)
	}
	if auth != nil {
		err = conn.Auth(auth)
		if err != nil {
			return apperror.NewError("authenticating SMTP client failed").AddError(err)
		}
	}
	err = conn.Mail(sender)
	if err != nil {
		return apperror.NewError("setting SMTP sender failed").AddError(err)
	}
	for _, addr := range to {
		err = conn.Rcpt(addr)
		if err != nil {
			return apperror.NewError("adding SMTP recipient failed").AddError(err)
		}
	}
	w, err := conn.Data()
	if err != nil {
		return apperror.NewError("creating SMTP data writer failed").AddError(err)
	}
	_, err = w.Write(raw)
	if err != nil {
		return apperror.NewError("writing SMTP data failed").AddError(err)
	}
	err = w.Close()
	if err != nil {
		return apperror.NewError("closing SMTP data writer failed").AddError(err)
	}
	err = conn.Quit()
	if err != nil {
		return apperror.NewError("quitting SMTP session failed").AddError(err)
	}
	return nil
}

// addPart attaches the part when its Content-Disposition marks it as an attachment and uses it as text or HTML body otherwise.
func (e *Email) addPart(p *part) error {
	contentType := p.header.Get(headerContentType)
	if contentType == "" {
		return apperror.NewError("no Content-Type found for MIME entity")
	}

	contentType, _, err := mime.ParseMediaType(p.header.Get(headerContentType))
	if err != nil {
		return apperror.NewErrorf("parsing Content-Type header with value %q failed", p.header.Get(headerContentType)).AddError(err)
	}

	attached, err := e.attachPart(p, contentType)
	if err != nil || attached {
		return err
	}

	switch contentType {
	case mediaTypeTextPlain:
		e.Text = p.body
	case mediaTypeTextHTML:
		e.HTML = p.body
	}
	return nil
}

// attachPart attaches the part if it is an attachment or an inline part with a file name and reports whether it did.
func (e *Email) attachPart(p *part, contentType string) (bool, error) {
	if p.header.Get(headerContentDisposition) == "" {
		return false, nil
	}

	cd, params, err := mime.ParseMediaType(p.header.Get(headerContentDisposition))
	if err != nil {
		return false, apperror.NewErrorf("parsing Content-Disposition header with value %q failed", p.header.Get(headerContentDisposition)).AddError(err)
	}
	filename, filenameDefined := params[paramFilename]
	if cd != dispositionAttachment && (cd != dispositionInline || !filenameDefined) {
		return false, nil
	}

	_, err = e.Attach(bytes.NewReader(p.body), filename, contentType)
	if err != nil {
		return false, apperror.NewErrorf("attaching MIME part %s failed", filename).AddError(err)
	}
	return true, nil
}

// setContentHeaders sets the content type of the message, w provides the boundary of a multipart message.
func (e *Email) setContentHeaders(headers textproto.MIMEHeader, layout multipartLayout, w *multipart.Writer) {
	switch {
	case layout.mixed:
		headers.Set(headerContentType, multipartMixedBoundary+w.Boundary())
	case layout.alternative:
		headers.Set(headerContentType, multipartAlternativeBoundary+w.Boundary())
	case layout.related:
		headers.Set(headerContentType, multipartRelatedBoundary+w.Boundary())
	case len(e.HTML) > 0:
		headers.Set(headerContentType, textHTMLUTF8)
		headers.Set(headerContentTransferEncoding, encodingQuotedPrintable)
	default:
		headers.Set(headerContentType, textPlainUTF8)
		headers.Set(headerContentTransferEncoding, encodingQuotedPrintable)
	}
}

// writeBody writes the text and HTML parts, w is the outermost multipart writer of the message.
func (e *Email) writeBody(buf io.Writer, w *multipart.Writer, layout multipartLayout, htmlAttachments []*Attachment) error {
	subWriter := w
	if layout.mixed && layout.alternative {
		subWriter = multipart.NewWriter(buf)
		header := textproto.MIMEHeader{
			headerContentType: {multipartAlternativeBoundary + subWriter.Boundary()},
		}
		_, err := w.CreatePart(header)
		if err != nil {
			return apperror.NewError("creating multipart/alternative part failed").AddError(err)
		}
	}

	if len(e.Text) > 0 {
		err := writeMessage(buf, e.Text, layout.mixed || layout.alternative, mediaTypeTextPlain, subWriter)
		if err != nil {
			return apperror.NewError("writing text part failed").AddError(err)
		}
	}

	if len(e.HTML) > 0 {
		err := e.writeHTMLPart(buf, w, subWriter, layout, htmlAttachments)
		if err != nil {
			return err
		}
	}

	if layout.mixed && layout.alternative {
		err := subWriter.Close()
		if err != nil {
			return apperror.NewError("closing multipart/alternative part failed").AddError(err)
		}
	}
	return nil
}

// writeHTMLPart writes the HTML body followed by its inline attachments, w is the outermost multipart writer
// and subWriter the one the HTML part belongs to.
func (e *Email) writeHTMLPart(buf io.Writer, w, subWriter *multipart.Writer, layout multipartLayout, htmlAttachments []*Attachment) error {
	messageWriter := subWriter
	var relatedWriter *multipart.Writer
	if (layout.mixed || layout.alternative) && len(htmlAttachments) > 0 {
		relatedWriter = multipart.NewWriter(buf)
		header := textproto.MIMEHeader{
			headerContentType: {multipartRelatedBoundary + relatedWriter.Boundary()},
		}
		_, err := subWriter.CreatePart(header)
		if err != nil {
			return apperror.NewError("creating multipart/related part failed").AddError(err)
		}
		messageWriter = relatedWriter
	}
	if layout.related && len(htmlAttachments) > 0 {
		relatedWriter = w
		messageWriter = w
	}

	err := writeMessage(buf, e.HTML, layout.mixed || layout.alternative || layout.related, mediaTypeTextHTML, messageWriter)
	if err != nil {
		return apperror.NewError("writing HTML part failed").AddError(err)
	}

	if len(htmlAttachments) == 0 {
		return nil
	}

	err = writeAttachmentParts(relatedWriter, htmlAttachments, "HTML attachment")
	if err != nil {
		return err
	}

	if layout.mixed || layout.alternative {
		err = relatedWriter.Close()
		if err != nil {
			return apperror.NewError("closing multipart/related part failed").AddError(err)
		}
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
		for _, h := range []string{headerReplyTo, headerTo, headerCc, headerFrom, headerSubject, headerDate, headerMessageID, headerMIMEVersion} {
			if v, ok := e.Headers[h]; ok {
				res[h] = v
			}
		}
	}
	if _, ok := res[headerReplyTo]; !ok && len(e.ReplyTo) > 0 {
		res.Set(headerReplyTo, strings.Join(e.ReplyTo, ", "))
	}
	if _, ok := res[headerTo]; !ok && len(e.To) > 0 {
		res.Set(headerTo, strings.Join(e.To, ", "))
	}
	if _, ok := res[headerCc]; !ok && len(e.Cc) > 0 {
		res.Set(headerCc, strings.Join(e.Cc, ", "))
	}
	if _, ok := res[headerSubject]; !ok && e.Subject != "" {
		res.Set(headerSubject, e.Subject)
	}
	if _, ok := res[headerMessageID]; !ok {
		id, err := generateMessageID()
		if err != nil {
			return nil, apperror.NewError("generating message ID failed").AddError(err)
		}
		res.Set(headerMessageID, id)
	}
	if _, ok := res[headerFrom]; !ok {
		res.Set(headerFrom, e.From)
	}
	if _, ok := res[headerDate]; !ok {
		res.Set(headerDate, time.Now().Format(time.RFC1123Z))
	}
	if _, ok := res[headerMIMEVersion]; !ok {
		res.Set(headerMIMEVersion, mimeVersion)
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
			return "", apperror.NewError("parsing sender address failed").AddError(err)
		}
		return sender.Address, nil
	}

	if e.From == "" {
		return "", nil
	}

	from, err := mail.ParseAddress(e.From)
	if err != nil {
		return "", apperror.NewError("parsing From address failed").AddError(err)
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

// writeAttachmentParts writes every attachment as a part of w, kind names the attachments in error messages.
func writeAttachmentParts(w *multipart.Writer, attachments []*Attachment, kind string) error {
	for _, a := range attachments {
		a.setDefaultHeaders()
		ap, err := w.CreatePart(a.Header)
		if err != nil {
			return apperror.NewErrorf("creating %s part failed", kind).AddError(err)
		}
		err = base64Wrap(ap, a.Content)
		if err != nil {
			return apperror.NewErrorf("writing %s content failed", kind).AddError(err)
		}
	}
	return nil
}

func writeMessage(buf io.Writer, msg []byte, multipart bool, mediaType string, w *multipart.Writer) error {
	if multipart {
		header := textproto.MIMEHeader{
			headerContentType:             {mediaType + charsetUTF8Suffix},
			headerContentTransferEncoding: {encodingQuotedPrintable},
		}
		_, err := w.CreatePart(header)
		if err != nil {
			return apperror.NewError("creating message part failed").AddError(err)
		}
	}

	qp := quotedprintable.NewWriter(buf)
	_, err := qp.Write(msg)
	if err != nil {
		return apperror.NewError("writing message failed").AddError(err)
	}
	err = qp.Close()
	if err != nil {
		return apperror.NewError("closing quoted-printable writer failed").AddError(err)
	}
	return nil
}

// parseMIMEParts will recursively walk a MIME entity and return a []mime.Part containing
// each (flattened) mime.Part found.
func parseMIMEParts(hs textproto.MIMEHeader, b io.Reader) ([]*part, error) {
	var ps []*part
	if _, ok := hs[headerContentType]; !ok {
		hs.Set(headerContentType, DefaultContentType)
	}
	ct, params, err := mime.ParseMediaType(hs.Get(headerContentType))
	if err != nil {
		return ps, apperror.NewErrorf("parsing Content-Type header with value %q failed", hs.Get(headerContentType)).AddError(err)
	}
	if strings.HasPrefix(ct, mediaTypeMultipartPrefix) {
		boundary, ok := params[paramBoundary]
		if !ok {
			return ps, apperror.NewError("no boundary found for multipart entity")
		}
		return parseMultipartParts(b, boundary)
	}

	switch hs.Get(headerContentTransferEncoding) {
	case encodingQuotedPrintable:
		b = quotedprintable.NewReader(b)
	case encodingBase64:
		b = base64.NewDecoder(base64.StdEncoding, b)
	}
	var buf bytes.Buffer
	_, err = io.Copy(&buf, b)
	if err != nil {
		return ps, apperror.NewError("copying part data failed").AddError(err)
	}
	ps = append(ps, &part{body: buf.Bytes(), header: hs})

	return ps, nil
}

// parseMultipartParts walks the parts of a multipart entity and returns them flattened.
func parseMultipartParts(b io.Reader, boundary string) ([]*part, error) {
	var ps []*part
	mr := multipart.NewReader(b, boundary)
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ps, apperror.NewError("reading multipart part failed").AddError(err)
		}
		sps, err := parseMultipartPart(p)
		if err != nil {
			return ps, err
		}
		ps = append(ps, sps...)
	}
	return ps, nil
}

// parseMultipartPart returns the flattened parts of one part of a multipart entity.
func parseMultipartPart(p *multipart.Part) ([]*part, error) {
	if _, ok := p.Header[headerContentType]; !ok {
		p.Header.Set(headerContentType, DefaultContentType)
	}
	subct, _, err := mime.ParseMediaType(p.Header.Get(headerContentType))
	if err != nil {
		return nil, apperror.NewErrorf("parsing Content-Type header with value %q failed", p.Header.Get(headerContentType)).AddError(err)
	}
	if strings.HasPrefix(subct, mediaTypeMultipartPrefix) {
		sps, err := parseMIMEParts(p.Header, p)
		if err != nil {
			return nil, apperror.NewError("parsing multipart parts failed").AddError(err)
		}
		return sps, nil
	}

	var reader io.Reader = p
	if p.Header.Get(headerContentTransferEncoding) == encodingBase64 {
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	}
	var buf bytes.Buffer
	_, err = io.Copy(&buf, reader)
	if err != nil {
		return nil, apperror.NewError("copying part data failed").AddError(err)
	}
	return []*part{{body: buf.Bytes(), header: p.Header}}, nil
}

// base64Wrap encodes the attachment content, and wraps it according to RFC 2045 standards (every 76 chars)
// The output is then written to the specified io.Writer
func base64Wrap(w io.Writer, b []byte) error {
	// 57 raw bytes per 76-byte base64 line.
	const maxRaw = 57
	// Buffer for each line, including trailing CRLF.
	buf := make([]byte, MaxLineLength+len("\r\n"))
	copy(buf[MaxLineLength:], "\r\n")
	for len(b) >= maxRaw {
		base64.StdEncoding.Encode(buf, b[:maxRaw])
		_, err := w.Write(buf)
		if err != nil {
			return apperror.NewError("writing base64 wrapped data failed").AddError(err)
		}
		b = b[maxRaw:]
	}
	if len(b) > 0 {
		out := buf[:base64.StdEncoding.EncodedLen(len(b))]
		base64.StdEncoding.Encode(out, b)
		out = append(out, "\r\n"...)
		_, err := w.Write(out)
		if err != nil {
			return apperror.NewError("writing base64 wrapped data failed").AddError(err)
		}
	}

	return nil
}

// headerToBytes renders "header" to "buff". If there are multiple values for a
// field, multiple "Field: value\r\n" lines will be emitted.
func headerToBytes(buff io.Writer, header textproto.MIMEHeader) error {
	for field, vals := range header {
		for _, subval := range vals {
			err := writeHeaderField(buff, field, subval)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// writeHeaderField writes a single "Field: value\r\n" line.
func writeHeaderField(buff io.Writer, field, value string) error {
	_, err := io.WriteString(buff, field)
	if err != nil {
		return apperror.NewError("writing header field failed").AddError(err)
	}
	_, err = io.WriteString(buff, ": ")
	if err != nil {
		return apperror.NewError("writing header field separator failed").AddError(err)
	}
	_, err = buff.Write([]byte(encodeHeaderValue(field, value)))
	if err != nil {
		return apperror.NewError("writing header field value failed").AddError(err)
	}
	_, err = io.WriteString(buff, "\r\n")
	if err != nil {
		return apperror.NewError("writing header field terminator failed").AddError(err)
	}
	return nil
}

// encodeHeaderValue leaves the content and address headers as they are and Q-encodes every other header.
func encodeHeaderValue(field, value string) string {
	switch field {
	case headerContentType, headerContentDisposition, headerFrom, headerTo, headerCc, headerBcc:
		return value
	default:
		return mime.QEncoding.Encode(charsetUTF8, value)
	}
}

func generateMessageID() (string, error) {
	t := time.Now().UnixNano()
	pid := os.Getpid()
	rint, err := rand.Int(rand.Reader, maxBigInt)
	if err != nil {
		return "", apperror.NewError("generating random integer failed").AddError(err)
	}
	h, err := os.Hostname()
	if err != nil {
		h = fallbackHostname
	}
	msgid := fmt.Sprintf("<%d.%d.%d@%s>", t, pid, rint, h)
	return msgid, nil
}

func handleAddressList(v []string) []string {
	res := []string{}
	for _, a := range v {
		w := strings.SplitSeq(a, ",")
		for addr := range w {
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
