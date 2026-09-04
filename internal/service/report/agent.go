package report

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
)

// reportAgent menangani query laporan keuangan & pemakaian (get_report).
type reportAgent struct {
	db *gorm.DB
	// systemPrompt adalah prompt skill agent ini (lihat prompt.go);
	// dipakai bila agent diberi LLM call sendiri.
	systemPrompt string
	txnRepo      repository.TransactionRepository
	logRepo      repository.StockLogRepository
	sender       agent.MessageSender
	log          *slog.Logger
}

func NewAgent(
	db *gorm.DB,
	txnRepo repository.TransactionRepository,
	logRepo repository.StockLogRepository,
	sender agent.MessageSender,
	logger *slog.Logger,
) agent.SubAgent {
	return &reportAgent{db: db, systemPrompt: reportSystemPrompt, txnRepo: txnRepo, logRepo: logRepo, sender: sender, log: logger}
}

func (a *reportAgent) Actions() []string {
	return []string{domain.ActionGetReport}
}

// SystemPrompt mengembalikan prompt skill milik agent ini.
func (a *reportAgent) SystemPrompt() string { return a.systemPrompt }

func (a *reportAgent) Handle(ctx context.Context, req agent.Request) error {
	return a.handleGetReport(ctx, req.Message, req.Chat, req.Action, req.IntentCost)
}

// handleGetReport menangani action get_report (query laporan).
func (a *reportAgent) handleGetReport(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, action domain.ServiceAction, intentCost float64) error {
	a.log.InfoContext(ctx, "handler: get_report", "params", action.Params)
	if !chat.Initialized {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.PreInitMessage, intentCost)
	}

	// Extract parameters dari params
	reportType := "summary" // default
	period := "today"       // default
	var itemFilter string
	var customDateRange string

	if rt, ok := action.Params["report_type"].(string); ok {
		reportType = rt
	}
	if p, ok := action.Params["period"].(string); ok {
		period = p
	}
	if filter, ok := action.Params["item_filter"].(string); ok {
		itemFilter = filter
	}
	if cdr, ok := action.Params["custom_date_range"].(string); ok {
		customDateRange = cdr
	}

	return a.generateReport(ctx, msg, action, reportType, period, itemFilter, customDateRange, intentCost)
}

// generateReport membuat laporan berdasarkan parameter yang diekstrak oleh LLM.
func (a *reportAgent) generateReport(ctx context.Context, msg entity.IncomingMessage, action domain.ServiceAction, reportType, periodType, itemFilter, customDateRange string, intentCost float64) error {
	// Parse period ke time range
	var from, to time.Time
	now := time.Now()

	switch periodType {
	case "today":
		from = startOfDay(now)
		to = now
	case "yesterday":
		from = startOfDay(now).AddDate(0, 0, -1)
		to = from.Add(24*time.Hour - time.Second)
	case "this_week":
		from = startOfWeek(now)
		to = now
	case "last_week":
		thisWeek := startOfWeek(now)
		from = thisWeek.AddDate(0, 0, -7)
		to = thisWeek.Add(-time.Second)
	case "this_month":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		to = now
	case "last_month":
		firstThis := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		from = firstThis.AddDate(0, -1, 0)
		to = firstThis.Add(-time.Second)
	case "custom":
		// Parse tanggal langsung dari parameter LLM (from_date, to_date)
		if fromDate, ok := action.Params["from_date"].(string); ok && fromDate != "" {
			parsedFrom, err := parseLLMDate(fromDate)
			if err == nil {
				from = parsedFrom
			} else {
				a.log.WarnContext(ctx, "gagal parse from_date dari LLM", "from_date", fromDate, "err", err)
				from = startOfDay(now)
			}
		} else if customDateRange != "" {
			// Fallback ke parsing manual jika from_date tidak ada
			a.log.InfoContext(ctx, "using custom_date_range fallback", "custom_date_range", customDateRange)
			// Untuk sementara gunakan today sebagai fallback
			from = startOfDay(now)
			to = now
		} else {
			// Fallback jika tidak ada from_date
			from = startOfDay(now)
		}

		if toDate, ok := action.Params["to_date"].(string); ok && toDate != "" {
			parsedTo, err := ParseLLMDateEnd(toDate)
			if err == nil {
				to = parsedTo
			} else {
				a.log.WarnContext(ctx, "gagal parse to_date dari LLM", "to_date", toDate, "err", err)
				to = now
			}
		} else if customDateRange != "" {
			// Gunakan same fallback untuk to_date
			to = now
		} else {
			// Fallback jika tidak ada to_date
			to = now
		}
	case "all":
		from = time.Time{}
		to = now
	default:
		// Default ke today
		from = startOfDay(now)
		to = now
	}

	// Generate report based on type
	switch reportType {
	case "summary", "income", "expense":
		summary, err := a.txnRepo.WithTx(a.db).Summary(ctx, msg.ChatID, from, to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query ringkasan", "err", err)
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengambil laporan.", intentCost)
		}

		// Convert reportType to metric
		var metric reportMetric
		switch reportType {
		case "income":
			metric = metricIncome
		case "expense":
			metric = metricExpense
		default:
			metric = metricSummary
		}

		periodLabel := formatPeriodLabel(periodType, from, to)
		periodStruct := period{from: from, to: to, label: periodLabel}
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, formatTxnReport(metric, periodStruct, summary), intentCost)

	case "expense_by_item":
		items, err := a.txnRepo.WithTx(a.db).ExpenseByItem(ctx, msg.ChatID, from, to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query per item", "err", err)
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengambil laporan.", intentCost)
		}
		periodLabel := formatPeriodLabel(periodType, from, to)
		periodStruct := period{from: from, to: to, label: periodLabel}
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, formatExpenseByItem(periodStruct, items), intentCost)

	case "consumption":
		moves, err := a.logRepo.WithTx(a.db).MovementsByChat(ctx, msg.ChatID, from, to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query pemakaian", "err", err)
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengambil laporan.", intentCost)
		}
		periodLabel := formatPeriodLabel(periodType, from, to)
		periodStruct := period{from: from, to: to, label: periodLabel}
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, formatConsumption(periodStruct, moves), intentCost)

	default:
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, tipe laporan tidak dikenali.", intentCost)
	}
}

// formatPeriodLabel membuat label untuk periode berdasarkan type dan date range
func formatPeriodLabel(period string, from, to time.Time) string {
	switch period {
	case "today":
		return "hari ini (" + formatDay(from) + ")"
	case "yesterday":
		return "kemarin"
	case "this_week":
		return "minggu ini"
	case "last_week":
		return "minggu lalu"
	case "this_month":
		return "bulan ini"
	case "last_month":
		return formatMonth(from)
	case "all":
		return "sejauh ini"
	case "custom":
		return fmt.Sprintf("%s - %s", from.Format("02/01/2006"), to.Format("02/01/2006"))
	default:
		return "periode ini"
	}
}
