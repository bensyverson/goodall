// Command gen writes the mailbox package's synthetic fixtures.
//
// Every message, address, domain and date here is invented; no real mail was
// read to build any of it. The generator exists because two of the formats
// cannot be typed by hand without arithmetic: a .emlx file opens with the
// message's byte count in a field of exactly ten characters, and an mbox that
// exercises CRLF handling has to carry the carriage returns byte for byte.
//
// It lives under testdata so the go tool never builds it with the package.
// Run it from examples/triage/mailbox:
//
//	go run ./testdata/gen -out testdata
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	out := flag.String("out", "testdata", "directory to write the fixtures into")
	flag.Parse()
	if err := write(*out); err != nil {
		log.Fatal(err)
	}
}

// utc builds an invented timestamp. Nothing here is a real received time.
func utc(month time.Month, day, hour, min int) time.Time {
	return time.Date(2024, month, day, hour, min, 0, 0, time.UTC)
}

const (
	appleAccount = "A1B2C3D4-0000-4000-8000-000000000001"
	appleInbox   = "E5F60718-0000-4000-8000-000000000002"
	appleSent    = "99AA0BCC-0000-4000-8000-000000000003"
)

func write(out string) error {
	for _, f := range []func(string) error{writeApple, writeMbox, writeMaildir} {
		if err := f(out); err != nil {
			return err
		}
	}
	return nil
}

// writeApple lays out a store root that carries every nesting the reader has
// to handle: a Data/<n>/<m>/Messages directory, a Data/Messages directory, and
// a second mailbox folder that an INBOX scan must leave alone.
func writeApple(out string) error {
	root := filepath.Join(out, "apple", "V10", appleAccount)
	inbox := filepath.Join(root, "INBOX.mbox", appleInbox)
	sent := filepath.Join(root, "Sent Messages.mbox", appleSent)

	files := []struct {
		path     string
		message  string
		received time.Time
		convID   int
		remoteID string
	}{
		{filepath.Join(inbox, "Data", "1", "2", "Messages", "101.emlx"), appleQuotedReply, utc(time.March, 5, 20, 51), 8801, "101"},
		{filepath.Join(inbox, "Data", "1", "2", "Messages", "102.partial.emlx"), applePartial, utc(time.March, 6, 20, 51), 8802, "102"},
		{filepath.Join(inbox, "Data", "Messages", "103.emlx"), appleNewsletter, utc(time.March, 4, 20, 51), 8803, "103"},
		{filepath.Join(sent, "Data", "Messages", "901.emlx"), appleSentReply, utc(time.March, 7, 20, 51), 8801, "901"},
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return err
		}
		body := emlx(f.message, f.received, f.convID, f.remoteID)
		if err := os.WriteFile(f.path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// emlx renders one .emlx file: the ten-character count line, the message
// bytes, then the property list Mail appends after them.
func emlx(message string, received time.Time, convID int, remoteID string) []byte {
	msg := []byte(message)
	count := fmt.Sprintf("%-10d", len(msg))
	if len(count) != 10 {
		panic("the count field must be exactly ten characters")
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>conversation-id</key>
	<integer>%d</integer>
	<key>date-last-viewed</key>
	<integer>%d</integer>
	<key>date-received</key>
	<integer>%d</integer>
	<key>flags</key>
	<integer>8623489089</integer>
	<key>remote-id</key>
	<string>%s</string>
</dict>
</plist>
`, convID, received.Add(time.Hour).Unix(), received.Unix(), remoteID)

	var b []byte
	b = append(b, count...)
	b = append(b, '\n')
	b = append(b, msg...)
	b = append(b, plist...)
	return b
}

const appleQuotedReply = `Return-path: <ann@northwind.example>
Received: from mx.northwind.example by mail.example.test with SMTP id 42
	for <you@example.test>; Tue, 5 Mar 2024 20:51:10 +0000
From: Ann Example <ann@northwind.example>
To: You <you@example.test>
Subject: =?utf-8?Q?Quarterly_caf=C3=A9_budget?=
Date: Tue, 5 Mar 2024 20:51:00 +0000
Message-ID: <apple-one@northwind.example>
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="alt-boundary-1"

--alt-boundary-1
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: 7bit

Can you approve the budget before Friday?

On Mon, 4 Mar 2024 at 09:15, You <you@example.test> wrote:
> What is the deadline on this?
> Any time next week works for me.

--
Ann Example
Northwind, Ltd.

--alt-boundary-1
Content-Type: text/html; charset="utf-8"
Content-Transfer-Encoding: 7bit

<html><body><p>Can you approve the budget before Friday?</p></body></html>

--alt-boundary-1--
`

// applePartial is what Mail leaves on disk when it has the headers and the
// start of the body but never fetched the rest: no closing boundary.
const applePartial = `Return-path: <ops@lighthouse.example>
From: "Ops Desk" <ops@lighthouse.example>
To: you@example.test
Subject: Disk space warning
Date: Wed, 6 Mar 2024 20:51:00 +0000
Message-ID: <apple-partial@lighthouse.example>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="mixed-boundary-2"

--mixed-boundary-2
Content-Type: text/plain; charset="us-ascii"

The backup volume is at 91 percent.
`

// appleNewsletter carries no Message-ID, so the reader has to mint a stable id
// of its own, and a quoted-printable body with a soft line break.
const appleNewsletter = `Return-path: <news@brightwater.example>
From: Brightwater Weekly <news@brightwater.example>
To: you@example.test
Subject: Five things this week
Date: Mon, 4 Mar 2024 20:51:00 +0000
MIME-Version: 1.0
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: quoted-printable

Five things worth reading, including a note on caf=C3=A9 culture and a=
 long line the encoder wrapped.
`

const appleSentReply = `Return-path: <you@example.test>
From: You <you@example.test>
To: Ann Example <ann@northwind.example>
Subject: Re: Quarterly budget
Date: Thu, 7 Mar 2024 20:51:00 +0000
Message-ID: <apple-sent@example.test>
MIME-Version: 1.0
Content-Type: text/plain; charset="us-ascii"

Approved, go ahead.
`

// writeMbox writes three messages: one whose body carries a From_-quoted line,
// one quoted-printable, and one with CRLF endings and no Date header.
func writeMbox(out string) error {
	crlf := strings.ReplaceAll(mboxThird, "\n", "\r\n")
	body := mboxFirst + mboxSecond + crlf
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "sample.mbox"), []byte(body), 0o644)
}

const mboxFirst = `From alice@example.test Wed Mar  6 08:00:00 2024
Return-path: <alice@example.test>
From: Alice Example <alice@example.test>
To: you@example.test
Subject: Trail notes
Date: Wed, 6 Mar 2024 08:00:00 +0000
Message-ID: <mbox-one@example.test>
MIME-Version: 1.0
Content-Type: text/plain; charset="us-ascii"

>From the top of the hill you can see the whole valley.
We should go back in spring.

--
Alice

`

const mboxSecond = `From bob@example.test Thu Mar  7 09:30:00 2024
Return-path: <bob@example.test>
From: Bob Example <bob@example.test>
To: you@example.test
Subject: =?ISO-8859-1?Q?Caf=E9_meeting?=
Date: Thu, 7 Mar 2024 09:30:00 +0000
Message-ID: <mbox-two@example.test>
MIME-Version: 1.0
Content-Type: text/plain; charset="utf-8"
Content-Transfer-Encoding: quoted-printable

Let's meet at the caf=C3=A9 on Thursday to go over the numbers=
 before the board call.

`

// mboxThird has no Date header, so its received time can only come from the
// From_ line, and it is written with CRLF endings.
const mboxThird = `From carol@example.test Tue Mar  5 07:15:00 2024
Return-path: <carol@example.test>
From: Carol Example <carol@example.test>
To: you@example.test
Subject: Bring the projector
Message-ID: <mbox-three@example.test>
MIME-Version: 1.0
Content-Type: text/plain; charset="us-ascii"

The room has no HDMI cable either.
`

// writeMaildir writes one message in cur, dated, one in new with no Date
// header so the reader has to fall back to the file's modification time, and a
// half-delivered one in tmp that the reader must ignore.
func writeMaildir(out string) error {
	root := filepath.Join(out, "maildir")
	for _, dir := range []string{"cur", "new", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		filepath.Join(root, "cur", "1709546400.M1P100.mail.example,S=512:2,S"): maildirCur,
		filepath.Join(root, "new", "1709550000.M2P200.mail.example"):           maildirNew,
		filepath.Join(root, "tmp", "1709553600.M3P300.mail.example"):           maildirTmp,
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

const maildirCur = `Return-path: <billing@sandpiper.example>
From: Sandpiper Billing <billing@sandpiper.example>
To: you@example.test
Subject: Invoice 2024-014
Date: Mon, 4 Mar 2024 10:00:00 +0000
Message-ID: <maildir-cur@sandpiper.example>
MIME-Version: 1.0
Content-Type: text/plain; charset="us-ascii"

Invoice 2024-014 is attached to your account and due in thirty days.
`

// maildirNew has no Date header on purpose.
const maildirNew = `Return-path: <notices@parcelworks.example>
From: Parcelworks <notices@parcelworks.example>
To: you@example.test
Subject: Package delivered
Message-ID: <maildir-new@parcelworks.example>
MIME-Version: 1.0
Content-Type: text/plain; charset="us-ascii"

Your package was left in the porch.
`

// maildirTmp is a delivery in progress: a reader that lists tmp would show it.
const maildirTmp = `From: Halfway Delivery <half@parcelworks.example>
To: you@example.test
Subject: Never delivered
Date: Fri, 8 Mar 2024 11:00:00 +0000
Message-ID: <maildir-tmp@parcelworks.example>
MIME-Version: 1.0
Content-Type: text/plain; charset="us-ascii"

This message is still being written.
`
