//go:build acceptance

package api

// The files attached to a mail, on a mailbox reached over IMAP.
//
// The reader shows a mail's attachments under its text, one chip each with
// its name, its type and its size, and a click on a chip saves the file
// (EmailReader.js). Over the Gmail API, Google hands the parts over already
// decoded. Over IMAP the files are inside the message itself, as its
// server keeps it: a multipart/mixed whose parts are encoded for transport
// (base64, quoted-printable) and whose names may be encoded twice over
// (RFC 2047 or RFC 2231 for an accented name). The mail of these sentences
// is written the way mail programs write one, and the file a person saves
// must be the one that was sent, byte for byte, under its own name.
//
// A download names a part of one mail. A part that mail does not have must
// get the sentence that says so and nothing else: not another part, not the
// text of the mail.
//
// Saving a file is not reading the mail. A plain BODY[] fetch sets \Seen on
// the server, so a mail the person put back to unread would turn read, on
// their phone as on the list after the next sync, just because they saved
// its invoice.
//
// The pictures an HTML mail draws in its text (a logo, a photo pasted in the
// message) are parts of the mail too, named by a cid: URL that no browser
// resolves. They are not files: the reader says which part each cid: is
// (inlineImages) and the screen downloads it to put it in place. Only a
// picture a browser shows without running anything: an SVG, a document that
// can carry script, is neither handed out nor served.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// invoiceMail is a shop's invoice, with its two files attached.
var invoiceMail = incoming{
	From:    "Boutique <factures@boutique.com>",
	Subject: "Votre facture de décembre",
	Body:    "Bonjour, vous trouverez ci-joint votre facture et le relevé de vos achats.",
}

// invoicePDF is the invoice itself: a binary file in base64, its accented
// name written the way Gmail and Outlook write it.
var invoicePDF = attachedFile{
	Name:     "Facture décembre.pdf",
	Type:     "application/pdf",
	Data:     samplePDF(),
	NameAs:   nameRFC2047,
	Transfer: transferBase64,
}

// purchasesCSV is the statement that comes with it: a text file in
// quoted-printable, its accented name written the way Thunderbird and Apple
// Mail write it.
var purchasesCSV = attachedFile{
	Name:     "Relevé d'achats été.csv",
	Type:     "text/csv",
	Data:     []byte("Date;Libellé;Montant\r\n02/12/2026;Café en grains;12,90\r\n05/12/2026;Théière;34,00\r\n"),
	NameAs:   nameRFC2231,
	Transfer: transferQuotedPrintable,
}

// attachmentNotInMail is what a download answers, as its "error", for a
// part the mail does not have.
const attachmentNotInMail = "Cette pièce jointe est introuvable dans ce mail."

// plainMail is a mail with no file attached.
var plainMail = incoming{
	From:    "Ma Banque <info@banque.fr>",
	Subject: "Votre conseiller vous répond",
	Body:    "Bonjour, votre rendez-vous est confirmé pour jeudi.",
}

// samplePDF is a small PDF whose content holds every byte value, the line
// breaks and NUL among them: a file that went through a text conversion,
// or was not decoded from its base64, does not come back equal.
func samplePDF() []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj\n<< /Length 512 >>\nstream\n")
	for round := 0; round < 2; round++ {
		for c := 0; c < 256; c++ {
			b.WriteByte(byte(c))
		}
	}
	b.WriteString("\nendstream\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	return b.Bytes()
}

// invoiceInInbox brings a person to where these sentences start: the
// invoice in their IMAP inbox, synced, and on the inbox screen. It returns
// the person and the invoice's number.
func invoiceInInbox(app *testApp) (*imapPerson, string) {
	app.t.Helper()
	moi := app.imapPerson(imapInboxOwner)
	uid := moi.receiveWithFiles(invoiceMail, invoicePDF, purchasesCSV)
	moi.sync()
	moi.requireShown(uid, invoiceMail.Subject)
	return moi, uid
}

// requireShown stops the scenario when the inbox screen does not list this
// mail: a sentence about what the reader shows of a mail proves nothing if
// the person cannot open it.
func (p *imapPerson) requireShown(uid, subject string) {
	p.app.t.Helper()
	if !p.shows(subject) {
		p.app.fatalf("après la synchronisation, la boîte de réception de %s devrait montrer le mail n°%s %q, elle ne le montre pas : le scénario ne prouve rien", p.email, uid, subject)
	}
}

// chipNamed returns the attachment the reader shows under this name.
func chipNamed(shown []shownAttachment, name string) (shownAttachment, bool) {
	for _, c := range shown {
		if c.Filename == name {
			return c, true
		}
	}
	return shownAttachment{}, false
}

// describeChips is the attachments of the reader in a failure message.
func describeChips(shown []shownAttachment) string {
	if len(shown) == 0 {
		return "aucune pièce jointe"
	}
	parts := make([]string, 0, len(shown))
	for _, c := range shown {
		parts = append(parts, fmt.Sprintf("« %s » (%s, %d octets)", c.Filename, c.MimeType, c.Size))
	}
	return strings.Join(parts, ", ")
}

// sameMediaType compares two types as a reader shows them: the media type
// alone, in any case, whatever parameter (a charset) comes after it.
func sameMediaType(got, want string) bool {
	t, _, err := mime.ParseMediaType(got)
	return err == nil && strings.EqualFold(t, want)
}

// firstDifference is where two files part ways, for a failure message.
func firstDifference(got, want []byte) int {
	for i := range got {
		if i >= len(want) || got[i] != want[i] {
			return i
		}
	}
	return len(got)
}

// leakedParts names the parts of the invoice and of plainMail found in a
// download's body, decoded or still encoded as the server keeps them. A
// download of a part the mail does not have must send none of them.
func leakedParts(body []byte) []string {
	marks := []struct {
		what  string
		marks []string
	}{
		// The start of the PDF, and the same start in base64.
		{fmt.Sprintf("le PDF « %s »", invoicePDF.Name), []string{"%PDF-", base64.StdEncoding.EncodeToString([]byte("%PDF-1.4"))[:8]}},
		// Its first line, the same decoded and in quoted-printable.
		{fmt.Sprintf("le tableau « %s »", purchasesCSV.Name), []string{"Date;Libell"}},
		{fmt.Sprintf("le texte du mail %q", invoiceMail.Subject), []string{invoiceMail.Body}},
		{fmt.Sprintf("le texte du mail %q", plainMail.Subject), []string{plainMail.Body}},
	}
	var found []string
	for _, m := range marks {
		for _, s := range m.marks {
			if bytes.Contains(body, []byte(s)) {
				found = append(found, m.what)
				break
			}
		}
	}
	return found
}

// newsletterMail is a shop's newsletter in HTML, which draws its logo and a
// badge in its text, by their cid:.
var newsletterMail = incoming{
	From:    "Boutique <nouveautes@boutique.com>",
	Subject: "Nos nouveautés de décembre",
	Body:    "Bonjour, voici nos nouveautés de décembre.",
	HTML: `<html><body><p><img src="cid:` + shopLogo.ContentID + `" alt="Boutique"></p>` +
		`<p>Bonjour, voici nos nouveautés de décembre.</p>` +
		`<p><img src="cid:` + shopBadge.ContentID + `" alt="Badge"></p></body></html>`,
}

// shopLogo is the logo the newsletter draws: a PNG without a name, its
// Content-ID in capitals as Apple Mail writes one.
var shopLogo = drawnPicture{
	ContentID: "6F0B3C2E-91D4-4A7B-B8E5-0C4D2A1F9E37@boutique.com",
	Type:      "image/png",
	Data:      samplePNG(),
}

// shopBadge is a picture the newsletter draws the same way, in SVG.
var shopBadge = drawnPicture{
	ContentID: "badge@boutique.com",
	Type:      "image/svg+xml",
	Data:      []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><script>/* badge du scenario */</script><rect width="16" height="16"/></svg>`),
}

// samplePNG is a small PNG of four pixels, each of another colour: bytes of
// every kind, a NUL among them, so a picture that went through a text
// conversion, or was not decoded from its base64, does not come back equal.
func samplePNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	img.Set(1, 0, color.NRGBA{G: 0xff, A: 0xff})
	img.Set(0, 1, color.NRGBA{B: 0xff, A: 0x80})
	img.Set(1, 1, color.NRGBA{A: 0x00})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// screenCIDKey is the key the screen looks a cid: URL up under in
// inlineImages (cidKey in EmailReader.js): unescaped, without angle
// brackets, in lower case.
func screenCIDKey(ref string) string {
	id := ref
	if unescaped, err := url.PathUnescape(ref); err == nil {
		id = unescaped
	}
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return strings.ToLower(strings.TrimSpace(id))
}

// screenDrawnTypes are the types of the pictures the screen draws
// (DRAWN_TYPES in EmailReader.js): it compares the type of the download, in
// lower case, to these, whole.
var screenDrawnTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/jpg": true,
	"image/gif": true, "image/webp": true, "image/bmp": true,
}

func TestScenarios_IMAPAttachments(t *testing.T) {
	playScenarios(t, "Les pièces jointes sur une boîte IMAP", []scenario{
		{
			phrase: "Sur une boîte IMAP, le lecteur d'un mail montre ses pièces jointes, avec leur nom, leur type et leur taille.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uid := invoiceInInbox(app)

				shown := moi.openInReader(uid)
				sent := []attachedFile{invoicePDF, purchasesCSV}
				for _, f := range sent {
					chip, ok := chipNamed(shown, f.Name)
					if !ok {
						app.failf("le lecteur du mail n°%s %q de %s devrait montrer la pièce jointe « %s », il ne la montre pas (il montre : %s)", uid, invoiceMail.Subject, moi.email, f.Name, describeChips(shown))
						continue
					}
					if !sameMediaType(chip.MimeType, f.Type) {
						app.failf("le lecteur du mail n°%s %q de %s devrait montrer « %s » avec son type, %s, il montre %q", uid, invoiceMail.Subject, moi.email, f.Name, f.Type, chip.MimeType)
					}
					if chip.Size != int64(len(f.Data)) {
						app.failf("le lecteur du mail n°%s %q de %s devrait montrer « %s » avec sa taille, %d octets, il montre %d octets", uid, invoiceMail.Subject, moi.email, f.Name, len(f.Data), chip.Size)
					}
				}
				// Nothing else: the text of the mail is not a file, and a
				// chip for it would offer to download what the reader
				// already shows.
				for _, c := range shown {
					if c.Filename != invoicePDF.Name && c.Filename != purchasesCSV.Name {
						app.failf("le lecteur du mail n°%s %q de %s ne devrait montrer que ses %d pièces jointes, il montre en plus « %s » (%s, %d octets)", uid, invoiceMail.Subject, moi.email, len(sent), c.Filename, c.MimeType, c.Size)
					}
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, télécharger une pièce jointe depuis le lecteur donne le fichier tel qu'il a été envoyé, octet pour octet, avec son nom.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uid := invoiceInInbox(app)

				shown := moi.openInReader(uid)
				for _, f := range []attachedFile{invoicePDF, purchasesCSV} {
					chip, ok := chipNamed(shown, f.Name)
					if !ok {
						app.failf("le lecteur du mail n°%s %q de %s ne propose pas de télécharger « %s » : il ne montre pas cette pièce jointe (il montre : %s)", uid, invoiceMail.Subject, moi.email, f.Name, describeChips(shown))
						continue
					}
					if chip.AttachmentID == "" {
						app.failf("le lecteur du mail n°%s %q de %s montre « %s » sans proposer de la télécharger (elle n'a pas d'attachmentId)", uid, invoiceMail.Subject, moi.email, f.Name)
						continue
					}

					// A click on the chip.
					got := moi.download(uid, chip.AttachmentID)
					if got.code != http.StatusOK {
						app.failf("télécharger « %s » depuis le lecteur du mail n°%s %q de %s devrait donner le fichier, le serveur a répondu %s", f.Name, uid, invoiceMail.Subject, moi.email, got)
						continue
					}
					if !bytes.Equal(got.body, f.Data) {
						app.failf("le fichier « %s » téléchargé depuis le lecteur du mail n°%s de %s devrait être celui envoyé, octet pour octet (%d octets) : il fait %d octets et diffère à partir de l'octet %d", f.Name, uid, moi.email, len(f.Data), len(got.body), firstDifference(got.body, f.Data))
					}
					// The name a browser saves it under: filename*, when
					// the answer gives one, wins over filename.
					disposition := got.header.Get("Content-Disposition")
					_, params, err := mime.ParseMediaType(disposition)
					if err != nil || params["filename"] != f.Name {
						app.failf("le fichier « %s » téléchargé depuis le lecteur du mail n°%s de %s devrait arriver sous son nom, la réponse le nomme %q (Content-Disposition : %q)", f.Name, uid, moi.email, params["filename"], disposition)
					}
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, télécharger une pièce jointe qui n'existe pas dans ce mail répond « Cette pièce jointe est introuvable dans ce mail. », sans rien envoyer d'autre.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				plain := moi.receive(plainMail)[0]
				uid := moi.receiveWithFiles(invoiceMail, invoicePDF, purchasesCSV)
				moi.sync()
				moi.requireShown(plain, plainMail.Subject)
				moi.requireShown(uid, invoiceMail.Subject)

				// The attachments the reader hands out for the invoice, so
				// none of them is taken for an invented one.
				real := map[string]bool{}
				for _, c := range moi.openInReader(uid) {
					if c.AttachmentID != "" {
						real[c.AttachmentID] = true
					}
				}

				type try struct{ mail, subject, id, what string }
				var tries []try
				// On the invoice, ids it has no file under: its text ("1",
				// the number of its first MIME part), a part past its last
				// one, and "TEXT", the name IMAP gives the whole body.
				for _, id := range []string{"1", "4", "TEXT"} {
					if !real[id] {
						tries = append(tries, try{uid, invoiceMail.Subject, id, "un identifiant sous lequel ce mail n'a pas de pièce jointe"})
					}
				}
				// On the mail without files: "1" is its text, and the
				// invoice's attachments are not its own.
				tries = append(tries, try{plain, plainMail.Subject, "1", "ce mail n'a aucune pièce jointe"})
				ids := make([]string, 0, len(real))
				for id := range real {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				for _, id := range ids {
					if id != "1" {
						tries = append(tries, try{plain, plainMail.Subject, id, fmt.Sprintf("une pièce jointe du mail %q, pas de celui-ci", invoiceMail.Subject)})
					}
				}

				for _, tr := range tries {
					got := moi.download(tr.mail, tr.id)
					if got.code < 400 || got.code > 499 || got.said() != attachmentNotInMail {
						app.failf("télécharger la pièce jointe %q du mail n°%s %q de %s (%s) devrait répondre %q avec une erreur 4xx, le serveur a répondu %s", tr.id, tr.mail, tr.subject, moi.email, tr.what, attachmentNotInMail, got)
					}
					if leaked := leakedParts(got.body); len(leaked) > 0 {
						app.failf("télécharger la pièce jointe %q du mail n°%s %q de %s (%s) ne devrait rien envoyer d'autre qu'une erreur, la réponse contient %s", tr.id, tr.mail, tr.subject, moi.email, tr.what, strings.Join(leaked, ", "))
					}
					if d, params, err := mime.ParseMediaType(got.header.Get("Content-Disposition")); err == nil && d == "attachment" {
						app.failf("télécharger la pièce jointe %q du mail n°%s %q de %s (%s) ne devrait proposer aucun fichier, la réponse propose %q", tr.id, tr.mail, tr.subject, moi.email, tr.what, params["filename"])
					}
				}
			},
		},
		{
			phrase: "Sur une boîte IMAP, télécharger la pièce jointe d'un mail que tu as remis en non lu le laisse non lu.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi, uid := invoiceInInbox(app)

				// Opened in the reader, which marks it read: on the server
				// and on the list. Both are read back here, so neither
				// check below passes by always saying "non lu".
				shown := moi.openInReader(uid)
				if !moi.seenOnServer(serverInbox, invoiceMail.Subject) {
					app.fatalf("après l'avoir ouvert dans la liseuse, le serveur IMAP de %s devrait montrer lu le mail n°%s %q, il le montre non lu : le scénario ne prouve rien", moi.email, uid, invoiceMail.Subject)
				}
				if row, ok := moi.shownRow(uid); !ok || !row.IsRead {
					app.fatalf("après l'avoir ouvert dans la liseuse, la boîte de réception de %s devrait montrer lu le mail n°%s %q (affiché : %v, lu : %v) : le scénario ne prouve rien", moi.email, uid, invoiceMail.Subject, ok, row.IsRead)
				}

				// "Non lu" in the reader, still open on the mail: the
				// single-message action with "unread" (flagAction in
				// Inbox.js). Where the sentence starts.
				moi.act(uid, "unread")
				if moi.seenOnServer(serverInbox, invoiceMail.Subject) {
					app.fatalf("après « Non lu », le serveur IMAP de %s devrait montrer non lu le mail n°%s %q, il le montre lu : le scénario ne prouve rien", moi.email, uid, invoiceMail.Subject)
				}
				if row, ok := moi.shownRow(uid); !ok || row.IsRead {
					app.fatalf("après « Non lu », la boîte de réception de %s devrait montrer non lu le mail n°%s %q (affiché : %v, lu : %v) : le scénario ne prouve rien", moi.email, uid, invoiceMail.Subject, ok, row.IsRead)
				}

				// A click on the chip of the PDF. It must go through: a
				// download that failed read nothing and proves nothing.
				chip, ok := chipNamed(shown, invoicePDF.Name)
				if !ok || chip.AttachmentID == "" {
					app.fatalf("le lecteur du mail n°%s %q de %s ne propose pas de télécharger « %s » (il montre : %s) : le scénario ne prouve rien", uid, invoiceMail.Subject, moi.email, invoicePDF.Name, describeChips(shown))
				}
				if got := moi.download(uid, chip.AttachmentID); got.code != http.StatusOK {
					app.fatalf("télécharger « %s » depuis le lecteur du mail n°%s de %s a répondu %s (attendu 200) : le scénario ne prouve rien", invoicePDF.Name, uid, moi.email, got)
				}

				// On the server, as the person's phone shows it.
				if moi.seenOnServer(serverInbox, invoiceMail.Subject) {
					app.failf("après le téléchargement de « %s », le serveur IMAP de %s devrait montrer encore non lu le mail n°%s %q, il le montre lu", invoicePDF.Name, moi.email, uid, invoiceMail.Subject)
				}
				// On the list, at once, then once "Synchroniser" has read
				// the server again: the list is served from what the last
				// sync stored.
				unreadOnList := func(when string) {
					t.Helper()
					switch row, ok := moi.shownRow(uid); {
					case !ok:
						app.failf("%s, la boîte de réception de %s devrait montrer encore le mail n°%s %q, elle ne le montre plus", when, moi.email, uid, invoiceMail.Subject)
					case row.IsRead:
						app.failf("%s, la boîte de réception de %s devrait montrer encore non lu le mail n°%s %q, elle le montre lu", when, moi.email, uid, invoiceMail.Subject)
					}
				}
				unreadOnList(fmt.Sprintf("juste après le téléchargement de « %s »", invoicePDF.Name))
				moi.sync()
				unreadOnList(fmt.Sprintf("après le téléchargement de « %s » et une synchronisation", invoicePDF.Name))
			},
		},
		{
			phrase: "Sur une boîte IMAP, le lecteur d'un mail HTML montre à leur place les images que le mail intègre, sans en faire des pièces jointes ; une image SVG n'est ni montrée ni téléchargeable.",
			status: validated,
			run: func(t *testing.T, app *testApp) {
				moi := app.imapPerson(imapInboxOwner)
				uid := moi.receiveHTMLWithPictures(newsletterMail, []drawnPicture{shopLogo, shopBadge}, invoicePDF)
				moi.sync()
				moi.requireShown(uid, newsletterMail.Subject)

				page := moi.readerPage(uid)

				// Not files: the reader's only chip is the invoice, a real
				// attachment, which stays one.
				if _, ok := chipNamed(page.Attachments, invoicePDF.Name); !ok {
					app.failf("le lecteur du mail n°%s %q de %s devrait montrer sa pièce jointe « %s », il ne la montre pas (il montre : %s)", uid, newsletterMail.Subject, moi.email, invoicePDF.Name, describeChips(page.Attachments))
				}
				for _, c := range page.Attachments {
					if c.Filename != invoicePDF.Name {
						app.failf("le lecteur du mail n°%s %q de %s ne devrait montrer en pièce jointe que « %s », il montre aussi « %s » (%s, %d octets) : les images du texte ne sont pas des pièces jointes", uid, newsletterMail.Subject, moi.email, invoicePDF.Name, c.Filename, c.MimeType, c.Size)
					}
				}

				// In its place: the text the reader renders still points at
				// the logo, and the reader says, under the key the screen
				// looks that cid: up under, which part to draw there.
				if !strings.Contains(page.BodyHTML, "cid:"+shopLogo.ContentID) {
					app.failf("le texte du mail n°%s %q que le lecteur de %s affiche devrait garder l'image cid:%s à sa place, il ne la contient plus", uid, newsletterMail.Subject, moi.email, shopLogo.ContentID)
				}
				id, ok := page.InlineImages[screenCIDKey(shopLogo.ContentID)]
				if !ok {
					app.failf("le lecteur du mail n°%s %q de %s devrait donner à l'écran l'image du texte cid:%s pour la montrer à sa place, il ne la donne pas (il donne : %v)", uid, newsletterMail.Subject, moi.email, shopLogo.ContentID, page.InlineImages)
				} else {
					// What the screen downloads to draw it.
					got := moi.download(uid, id)
					switch {
					case got.code != http.StatusOK:
						app.failf("l'image cid:%s du mail n°%s %q de %s devrait se télécharger pour être montrée dans le texte, le serveur a répondu %s", shopLogo.ContentID, uid, newsletterMail.Subject, moi.email, got)
					case !screenDrawnTypes[strings.ToLower(got.header.Get("Content-Type"))]:
						app.failf("l'image cid:%s du mail n°%s %q de %s arrive avec le type %q, que l'écran ne dessine pas : il attend exactement %s", shopLogo.ContentID, uid, newsletterMail.Subject, moi.email, got.header.Get("Content-Type"), shopLogo.Type)
					case !bytes.Equal(got.body, shopLogo.Data):
						app.failf("l'image cid:%s du mail n°%s %q de %s devrait être celle du mail, octet pour octet (%d octets) : elle fait %d octets et diffère à partir de l'octet %d", shopLogo.ContentID, uid, newsletterMail.Subject, moi.email, len(shopLogo.Data), len(got.body), firstDifference(got.body, shopLogo.Data))
					}
				}

				// The SVG the text points at the same way: not handed to the
				// screen, and served under none of the mail's part numbers
				// (the ids the reader hands out, see receiveHTMLWithPictures),
				// its own (1.2.3) least of all.
				if _, ok := page.InlineImages[screenCIDKey(shopBadge.ContentID)]; ok {
					app.failf("le lecteur du mail n°%s %q de %s ne devrait pas donner à l'écran l'image SVG cid:%s, une image qui peut porter du script, il la donne", uid, newsletterMail.Subject, moi.email, shopBadge.ContentID)
				}
				svgMarks := [][]byte{[]byte("badge du scenario"), []byte(base64.StdEncoding.EncodeToString(shopBadge.Data)[:24])}
				for _, part := range []string{"1", "1.1", "1.2", "1.2.1", "1.2.2", "1.2.3", "1.2.4", "2", "3", "TEXT"} {
					got := moi.download(uid, part)
					for _, mark := range svgMarks {
						if bytes.Contains(got.body, mark) {
							app.failf("télécharger la partie %q du mail n°%s %q de %s ne devrait jamais donner l'image SVG cid:%s, la réponse la contient (%s)", part, uid, newsletterMail.Subject, moi.email, shopBadge.ContentID, got)
							break
						}
					}
					if part == "1.2.3" && (got.code < 400 || got.code > 499) {
						app.failf("télécharger la partie 1.2.3 du mail n°%s %q de %s, l'image SVG cid:%s, devrait être refusé, le serveur a répondu %s", uid, newsletterMail.Subject, moi.email, shopBadge.ContentID, got)
					}
				}
			},
		},
	})
}
