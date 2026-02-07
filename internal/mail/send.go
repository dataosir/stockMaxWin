// Package mail 按 SMTP 配置发送选股结果 HTML 邮件。
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"stockMaxWin/internal/model"
	"stockMaxWin/internal/trace"
)

const (
	smtpTimeout     = 15 * time.Second
	defaultSMTPPort = 587
)

type SMTPConfig struct {
	Server   string
	Port     int
	User     string
	Password string
	From     string
	To       string
}

func (s *SMTPConfig) Enabled() bool {
	return strings.TrimSpace(s.Server) != "" &&
		strings.TrimSpace(s.From) != "" &&
		strings.TrimSpace(s.To) != ""
}

func SendReport(ctx context.Context, cfg *SMTPConfig, stocks []*model.Stock) error {
	if cfg == nil || !cfg.Enabled() {
		return nil
	}
	if len(stocks) == 0 {
		return nil
	}
	trace.Log(ctx, "mail: SendReport to=%s count=%d", cfg.To, len(stocks))
	body := buildHTMLTable(stocks)
	subject := "今日选股结果"
	toList := strings.Split(cfg.To, ",")
	for i := range toList {
		toList[i] = strings.TrimSpace(toList[i])
	}
	err := send(cfg, subject, body, toList)
	if err != nil {
		trace.Log(ctx, "mail: send err=%v", err)
		return err
	}
	trace.Log(ctx, "mail: sent ok")
	return nil
}

func buildHTMLTable(stocks []*model.Stock) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="UTF-8"><title>选股结果</title></head><body>`)
	b.WriteString(`<h2>今日选股结果（按涨幅排序取前10）</h2><p>剔除ST/退市·市值&gt;50亿·PE 0-60·站上MA20·MA60向上·MACD红柱增或金叉·换手3%-10%·量比&gt;1.2。</p>`)
	b.WriteString(`<table border="1" cellspacing="0" cellpadding="8" style="border-collapse: collapse; font-size: 14px;">`)
	b.WriteString(`<thead><tr style="background: #eee;"><th>代码</th><th>名称</th><th>涨幅%</th><th>主营领域</th></tr></thead><tbody>`)
	for _, s := range stocks {
		if s == nil {
			continue
		}
		mb := s.MainBusiness
		if mb == "" {
			mb = "-"
		}
		b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%.2f</td><td>%s</td></tr>",
			escapeHTML(s.Code), escapeHTML(s.Name), s.ChangePct, escapeHTML(mb)))
	}
	b.WriteString("</tbody></table></body></html>")
	return b.String()
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func send(cfg *SMTPConfig, subject, htmlBody string, to []string) error {
	port := cfg.Port
	if port == 0 {
		port = defaultSMTPPort
	}
	addr := net.JoinHostPort(cfg.Server, strconv.Itoa(port))

	var conn net.Conn
	var err error
	if port == 465 {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: smtpTimeout}, "tcp", addr, &tls.Config{ServerName: cfg.Server})
	} else {
		conn, err = net.DialTimeout("tcp", addr, smtpTimeout)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.Server)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Server}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	if cfg.Password != "" {
		auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Server)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, t := range to {
		if t == "" {
			continue
		}
		if err := client.Rcpt(t); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", t, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n",
		cfg.From, strings.Join(to, ","), subject)
	if _, err := w.Write([]byte(headers + htmlBody)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return client.Quit()
}

func MustSendReport(ctx context.Context, cfg *SMTPConfig, stocks []*model.Stock) {
	if len(stocks) == 0 {
		trace.Log(ctx, "mail: 无选中股票，按设计不发邮件（正常）")
		return
	}
	if cfg == nil || !cfg.Enabled() {
		trace.Log(ctx, "mail: 未配置 SMTP，跳过")
		return
	}
	if err := SendReport(ctx, cfg, stocks); err != nil {
		trace.Log(ctx, "mail: 发送失败 err=%v", err)
		return
	}
	trace.Log(ctx, "mail: 已发送 to=%s count=%d", cfg.To, len(stocks))
}
