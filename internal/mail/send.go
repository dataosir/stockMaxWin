// Package mail 按 SMTP 配置发送选股结果 HTML 邮件。
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"stockMaxWin/internal/model"
	"stockMaxWin/internal/trace"
)

// 炒股格言（随机发一句，提醒无入选时也保持状态）
var stockMaxims = []string{
	"在别人恐惧时贪婪，在别人贪婪时恐惧。——巴菲特",
	"价格是你付出的，价值是你得到的。——巴菲特",
	"投资最重要的两条：第一，永远不要亏钱；第二，永远不要忘记第一条。——巴菲特",
	"不打算持有十年的股票，不要持有十分钟。——巴菲特",
	"买你理解的东西，不买你不理解的。——彼得·林奇",
	"短期市场是投票机，长期是称重机。——格雷厄姆",
	"市场短期是投票机，长期是称重机。——本杰明·格雷厄姆",
	"风险来自你不知道自己在做什么。——巴菲特",
	"宁可错过，不可做错。没有符合条件就不出手。——常见纪律",
	"会空仓的是师爷，会等待的是高手。",
}

// 超时与端口
const (
	smtpTimeout      = 15 * time.Second
	defaultSMTPPort  = 587
	smtpPortTLS      = 465
)

// 邮件主题与内容
const (
	subjectReport      = "今日选股结果"
	subjectNoSelection = "选股提醒：本期无入选，请好好工作"
	titleReport        = "选股结果"
	titleNoSelection   = "选股提醒"
	htmlCharset        = "UTF-8"
	emptyMainBusiness  = "-"
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
	subject := subjectReport
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
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="` + htmlCharset + `"><title>` + titleReport + `</title></head><body>`)
	b.WriteString(`<h2>今日选股结果（按涨幅排序取前10）</h2><p>剔除ST/退市·市值&gt;50亿·PE 0-60·站上MA20·MA60向上·MACD红柱增或金叉·换手3%-10%·量比&gt;1.2。</p>`)
	b.WriteString(`<table border="1" cellspacing="0" cellpadding="8" style="border-collapse: collapse; font-size: 14px;">`)
	b.WriteString(`<thead><tr style="background: #eee;"><th>代码</th><th>名称</th><th>涨幅%</th><th>主营领域</th></tr></thead><tbody>`)
	for _, s := range stocks {
		if s == nil {
			continue
		}
		mb := s.MainBusiness
		if mb == "" {
			mb = emptyMainBusiness
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
	if port == smtpPortTLS {
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

	if port != smtpPortTLS {
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
	if cfg == nil || !cfg.Enabled() {
		if len(stocks) == 0 {
			trace.Log(ctx, "mail: 无选中且未配置 SMTP，跳过")
		}
		return
	}
	if len(stocks) == 0 {
		trace.Log(ctx, "mail: 无选中股票，按设计不发邮件（正常）")
		return
	}
	if err := SendReport(ctx, cfg, stocks); err != nil {
		trace.Log(ctx, "mail: 发送失败 err=%v", err)
		return
	}
	trace.Log(ctx, "mail: 已发送 to=%s count=%d", cfg.To, len(stocks))
}

// SendNoSelectionReminder 连续多次无入选时发送提醒：本期没有入选股票，请好好工作 + 随机一句炒股格言。
func SendNoSelectionReminder(ctx context.Context, cfg *SMTPConfig) error {
	if cfg == nil || !cfg.Enabled() {
		return nil
	}
	quote := stockMaxims[rand.Intn(len(stockMaxims))]
	trace.Log(ctx, "mail: 发送无入选提醒，格言=%s", quote)
	body := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="%s"><title>%s</title></head><body>
<h2>本期没有入选股票</h2>
<p>请好好工作，耐心等待符合条件的机会。</p>
<p style="margin-top:16px;color:#666;font-style:italic;">%s</p>
</body></html>`, htmlCharset, titleNoSelection, escapeHTML(quote))
	subject := subjectNoSelection
	toList := strings.Split(cfg.To, ",")
	for i := range toList {
		toList[i] = strings.TrimSpace(toList[i])
	}
	return send(cfg, subject, body, toList)
}
