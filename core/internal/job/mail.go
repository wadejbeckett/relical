package job

import (
	"context"
	"errors"
	"io/fs"

	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/locator"
)

// CommonExcludes are Dovecot's rebuildable index and cache files.
var CommonExcludes = []string{"**/dovecot.index*", "**/dovecot.list.index*"}

// Mailboxes lists the mailboxes of every mail domain of the subscription.
// A mail domain with no mail directory has no mailboxes.
func (e Env) Mailboxes(sub inventory.Subscription) ([]locator.Mailbox, error) {
	all := []locator.Mailbox{}
	for _, d := range sub.MailDomains {
		boxes, err := e.Locator.Mailboxes(d)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		all = append(all, boxes...)
	}
	return all, nil
}

// NoMailboxesNote is the note of a mail backup of a subscription whose
// mail domains have no mailbox.
const NoMailboxesNote = "no mailboxes"

// BackupMail backs up the subscription's mail, across all its mail
// domains, into one snapshot tagged mail in the subscription's repository.
func (e Env) BackupMail(ctx context.Context, sub inventory.Subscription) Result {
	res := e.result(sub.Name, MailTag, "backup")
	if !sub.Has(MailTag) {
		res.OK = true
		res.Note = "mail is not among the subscription's features; nothing to back up"
		return res
	}
	boxes, err := e.Mailboxes(sub)
	if err != nil {
		return failed(res, err)
	}
	if len(boxes) == 0 {
		// Panels switch the mail service on by default, so a website-only
		// subscription often has mail domains without a single mailbox.
		res.OK = true
		res.Note = NoMailboxesNote
		return res
	}
	r, err := e.openRepo(ctx, sub)
	if err != nil {
		return failed(res, err)
	}
	paths := []string{}
	excludes := append([]string{}, CommonExcludes...)
	for _, b := range boxes {
		paths = append(paths, b.Path)
		p := GlobEscape(b.Path)
		excludes = append(excludes, p+"/tmp/*", p+"/.*/tmp/*")
	}
	sum, err := r.Backup(ctx, paths, []string{MailTag}, excludes)
	res = saved(res, sum, err)
	if res.OK {
		res.Mailboxes = len(boxes)
	}
	return res
}
