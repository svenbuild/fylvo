package email

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/wneessen/go-mail"
)

// SMTPPool sends mail over SMTP
type SMTPPool struct {
	// Deprecated
	Config SMTPConfig

	config *setting.SMTP
	ch     chan *message
	chOpen bool
	l      logging.Logger
}

// SMTPConfig is the SMTP sending configuration
type SMTPConfig struct {
	Name       string // sender name
	Address    string // sender address
	ReplyTo    string // reply-to address
	Host       string // server host name
	Port       int    // server port
	User       string // user name
	Password   string // password
	Encryption bool   // whether encryption is enabled
	Keepalive  int    // how long SMTPPool keeps a connection
}

type message struct {
	msg     *mail.Msg
	to      string
	subject string
	cid     string
	userID  int
}

// NewSMTPPool initializes a new SMTP based email sending queue.
func NewSMTPPool(config setting.Provider, logger logging.Logger) *SMTPPool {
	client := &SMTPPool{
		config: config.SMTP(context.Background()),
		ch:     make(chan *message, 30),
		chOpen: false,
		l:      logger,
	}

	client.Init()
	return client
}

// NewSMTPClient creates a new SMTP send queue
// Deprecated
func NewSMTPClient(config SMTPConfig) *SMTPPool {
	client := &SMTPPool{
		Config: config,
		ch:     make(chan *message, 30),
		chOpen: false,
	}

	client.Init()

	return client
}

// Send sends an email
func (client *SMTPPool) Send(ctx context.Context, to, title, body string) error {
	if !client.chOpen {
		return fmt.Errorf("SMTP pool is closed")
	}

	// ignore mailboxes that log in through QQ
	if strings.HasSuffix(to, "@login.qq.com") {
		return nil
	}

	m := mail.NewMsg()
	if err := m.FromFormat(client.config.FromName, client.config.From); err != nil {
		return err
	}
	m.ReplyToFormat(client.config.FromName, client.config.ReplyTo)
	m.To(to)
	m.Subject(title)
	m.SetMessageID()
	m.SetBodyString(mail.TypeTextHTML, body)
	client.ch <- &message{
		msg:     m,
		subject: title,
		to:      to,
		cid:     logging.CorrelationID(ctx).String(),
		userID:  inventory.UserIDFromContext(ctx),
	}
	return nil
}

// Close closes the send queue
func (client *SMTPPool) Close() {
	if client.ch != nil {
		close(client.ch)
	}
}

// Init initializes the send queue
func (client *SMTPPool) Init() {
	go func() {
		client.l.Info("Initializing and starting SMTP email pool...")
		defer func() {
			if err := recover(); err != nil {
				client.chOpen = false
				client.l.Error("Exception while sending email: %s, queue will be reset in 10 seconds.", err)
				time.Sleep(time.Duration(10) * time.Second)
				client.Init()
			}
		}()

		opts := []mail.Option{
			mail.WithPort(client.config.Port),
			mail.WithTimeout(time.Duration(client.config.Keepalive+5) * time.Second),
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover), mail.WithTLSPortPolicy(mail.TLSOpportunistic),
			mail.WithUsername(client.config.User), mail.WithPassword(client.config.Password),
		}
		if client.config.ForceEncryption {
			opts = append(opts, mail.WithSSL())
		}

		d, diaErr := mail.NewClient(client.config.Host, opts...)
		if diaErr != nil {
			client.l.Panic("Failed to create SMTP client: %s", diaErr)
			return
		}

		client.chOpen = true

		var err error
		open := false
		for {
			select {
			case m, ok := <-client.ch:
				if !ok {
					client.l.Info("Email queue closing...")
					client.chOpen = false
					return
				}

				if !open {
					if err = d.DialWithContext(context.Background()); err != nil {
						panic(err)
					}
					open = true
				}

				l := client.l.CopyWithPrefix(fmt.Sprintf("[Cid: %s]", m.cid))
				if err := d.Send(m.msg); err != nil {
					// Check if this is an SMTP RESET error after successful delivery
					var sendErr *mail.SendError
					var errParsed = errors.As(err, &sendErr)
					if errParsed && sendErr.Reason == mail.ErrSMTPReset {
						open = false
						l.Debug("SMTP RESET error, closing connection...")
						// https://github.com/wneessen/go-mail/issues/463
						continue // Don't treat this as a delivery failure since mail was sent
					}

					l.Warning("Failed to send email: %s, Cid=%s", err, m.cid)
				} else {
					l.Info("Email sent to %q, title: %q.", m.to, m.subject)
				}
			// no new mail for a long time, close the SMTP connection
			case <-time.After(time.Duration(client.config.Keepalive) * time.Second):
				if open {
					if err := d.Close(); err != nil {
						client.l.Warning("Failed to close SMTP connection: %s", err)
					}
					open = false
				}
			}
		}
	}()
}
