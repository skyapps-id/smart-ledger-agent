// Package goods menyediakan agent pengelola master barang per chat:
// daftar/detail katalog, satuan kanonik, dan faktor konversi antar satuan.
package goods

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
)

// goodsAgent menangani action `goods` (master barang & satuan).
type goodsAgent struct {
	db           *gorm.DB
	systemPrompt string
	goodsRepo    repository.GoodsRepository
	invRepo      repository.InventoryRepository
	pending      *agent.PendingConfirms
	sender       agent.MessageSender
	log          *slog.Logger
}

func NewAgent(
	db *gorm.DB,
	goodsRepo repository.GoodsRepository,
	invRepo repository.InventoryRepository,
	pending *agent.PendingConfirms,
	sender agent.MessageSender,
	logger *slog.Logger,
) agent.SubAgent {
	return &goodsAgent{
		db:           db,
		systemPrompt: goodsSystemPrompt,
		goodsRepo:    goodsRepo,
		invRepo:      invRepo,
		pending:      pending,
		sender:       sender,
		log:          logger,
	}
}

func (a *goodsAgent) Actions() []string {
	return []string{domain.ActionGoods}
}

// SystemPrompt mengembalikan prompt skill milik agent ini.
func (a *goodsAgent) SystemPrompt() string { return a.systemPrompt }

func (a *goodsAgent) Handle(ctx context.Context, req agent.Request) error {
	return a.handleGoodsAction(ctx, req.Message, req.Chat, req.Action.Params, req.IntentCost)
}

// handleGoodsAction menangani action goods dari LLM intent classifier.
func (a *goodsAgent) handleGoodsAction(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, params map[string]interface{}, intentCost float64) error {
	a.log.InfoContext(ctx, "handler: goods", "params", params)
	if !chat.Initialized {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.PreInitMessage, intentCost)
	}

	actionType, _ := params["goods_action"].(string)
	if actionType == "" {
		actionType = "list"
	}

	switch actionType {
	case "list":
		return a.handleList(ctx, msg, intentCost)
	case "add", "create":
		return a.handleAdd(ctx, msg, params, intentCost)
	case "info":
		return a.handleInfo(ctx, msg, params, intentCost)
	case "set_factor":
		return a.handleSetFactor(ctx, msg, params, intentCost)
	case "set_uom":
		return a.handleSetUom(ctx, msg, params, intentCost)
	case "set_category":
		return a.handleSetCategory(ctx, msg, params, intentCost)
	default:
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			"Aksi tidak dikenali. Ketik \"master barang\" untuk melihat katalog, \"set 1 [barang] [angka][satuan]\" untuk konversi, atau \"set kategori [barang] jadi [kategori]\".", intentCost)
	}
}

// handleAdd menambahkan barang baru ke master chat beserta satuan kanonik
// dan (opsional) faktor konversinya dalam satu perintah, mis.
// "tambah barang galon, satuan galon, 1 galon = 15lt". Barang yang sudah
// ada di-update (idempoten), bukan diduplikasi.
func (a *goodsAgent) handleAdd(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, intentCost float64) error {
	itemName, _ := params["item_name"].(string)
	unit, _ := params["unit"].(string)
	unit = strings.ToLower(strings.TrimSpace(unit))
	factorQty, _ := params["factor_qty"].(float64)
	factorUnit, _ := params["factor_unit"].(string)
	factorUnit = strings.ToLower(strings.TrimSpace(factorUnit))
	category, _ := params["category"].(string)
	category = strings.ToUpper(strings.TrimSpace(category))
	itemName = strings.TrimSpace(itemName)

	if itemName == "" || unit == "" {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			"Format: \"tambah barang [nama] satuan [satuan]\" (opsional: \", 1 [satuan] = [angka][satuan]\"). Contoh: tambah barang galon satuan galon, 1 galon = 15lt", intentCost)
	}

	// Cek dulu supaya balasan bisa membedakan "dibuat" vs "diperbarui".
	existing, err := a.goodsRepo.WithTx(a.db).GetByName(ctx, msg.ChatID, itemName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		a.log.ErrorContext(ctx, "gagal lookup goods", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal menyimpan barang.", intentCost)
	}
	created := existing == nil

	g, err := a.goodsRepo.WithTx(a.db).GetOrCreateByName(ctx, msg.ChatID, itemName, unit)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal create goods", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal menyimpan barang.", intentCost)
	}
	if !created {
		// Barang sudah ada: samakan satuan kanoniknya bila disebut.
		if err := a.goodsRepo.WithTx(a.db).UpdateUom(ctx, g.ID, unit); err != nil {
			a.log.ErrorContext(ctx, "gagal update uom", "err", err)
		}
	}

	factorInfo := ""
	if factorQty > 0 && factorUnit != "" {
		if err := a.goodsRepo.WithTx(a.db).UpdateConversion(ctx, g.ID, factorUnit, factorQty); err != nil {
			a.log.ErrorContext(ctx, "gagal simpan faktor konversi", "err", err)
		}
		factorInfo = fmt.Sprintf("\nKonversi: 1 %s = %g %s", unit, factorQty, factorUnit)
	}
	categoryInfo := ""
	if category != "" {
		if err := a.goodsRepo.WithTx(a.db).UpdateCategory(ctx, g.ID, category); err != nil {
			a.log.ErrorContext(ctx, "gagal simpan kategori", "err", err)
		}
		categoryInfo = fmt.Sprintf("\nKategori: %s", category)
	}

	verb := "ditambahkan"
	if !created {
		verb = "diperbarui"
	}
	a.log.InfoContext(ctx, "goods add", "item", g.Name, "created", created, "uom", unit, "factor", factorQty, "factor_unit", factorUnit, "category", category)
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
		fmt.Sprintf("✅ Barang %s %s:\nNama   : %s\nSatuan : %s%s%s", g.Name, verb, g.Name, unit, factorInfo, categoryInfo), intentCost)
}

// handleSetCategory mengubah kategori kanonik barang — sekali di-set,
// seluruh transaksi barang itu memakai kategori ini (tidak bergeser oleh
// hasil klasifikasi LLM per transaksi).
func (a *goodsAgent) handleSetCategory(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, intentCost float64) error {
	itemName, _ := params["item_name"].(string)
	category, _ := params["category"].(string)
	category = strings.ToUpper(strings.TrimSpace(category))

	if itemName == "" || category == "" {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			"Format: \"set kategori [barang] jadi [kategori]\" (contoh: set kategori galon jadi MINUMAN)", intentCost)
	}

	g, stop, err := a.resolveGood(ctx, msg, params, itemName, intentCost)
	if stop {
		return err
	}

	if err := a.goodsRepo.WithTx(a.db).UpdateCategory(ctx, g.ID, category); err != nil {
		a.log.ErrorContext(ctx, "gagal ubah kategori", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengubah kategori.", intentCost)
	}

	a.log.InfoContext(ctx, "kategori kanonik diubah", "item", g.Name, "category", category)
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
		fmt.Sprintf("✅ Kategori %s diubah ke %s. Transaksi berikutnya otomatis memakai kategori ini.", g.Name, category), intentCost)
}

// handleList menampilkan seluruh master barang chat dengan satuan & faktor
// konversinya (bila sudah dipelajari).
func (a *goodsAgent) handleList(ctx context.Context, msg entity.IncomingMessage, intentCost float64) error {
	items, err := a.goodsRepo.WithTx(a.db).ListByChat(ctx, msg.ChatID)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal query goods", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengambil master barang.", intentCost)
	}
	if len(items) == 0 {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			"Belum ada barang di master. Barang otomatis tercatat saat kamu belanja (contoh: \"beli beras 5kg 75rb\").", intentCost)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📦 Master Barang (%d):\n", len(items))
	for _, g := range items {
		label := g.Name
		if g.Category != "" {
			label = fmt.Sprintf("[%s] %s", g.Category, g.Name)
		}
		if g.FactorUom > 0 && g.ConversionUom != "" {
			fmt.Fprintf(&b, "- %s (1 %s = %g %s)\n", label, uomOrDefault(g.Uom), g.FactorUom, g.ConversionUom)
			continue
		}
		fmt.Fprintf(&b, "- %s (%s)\n", label, uomOrDefault(g.Uom))
	}
	b.WriteString("\n💡 Atur konversi: \"set 1 [barang] [angka][satuan]\" (contoh: set 1 galon 15lt)")
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, b.String(), intentCost)
}

// handleInfo menampilkan detail satu barang: satuan kanonik, faktor
// konversi, dan stok saat ini (bila barang pernah dibeli).
func (a *goodsAgent) handleInfo(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, intentCost float64) error {
	itemName, _ := params["item_name"].(string)
	if itemName == "" {
		return a.handleList(ctx, msg, intentCost)
	}

	g, stop, err := a.resolveGood(ctx, msg, params, itemName, intentCost)
	if stop {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📦 %s\n", g.Name)
	fmt.Fprintf(&b, "Kode    : %s\n", g.Code)
	fmt.Fprintf(&b, "Kategori: %s\n", categoryOrDefault(g.Category))
	fmt.Fprintf(&b, "Satuan  : %s\n", uomOrDefault(g.Uom))
	if g.FactorUom > 0 && g.ConversionUom != "" {
		fmt.Fprintf(&b, "Konversi: 1 %s = %g %s\n", uomOrDefault(g.Uom), g.FactorUom, g.ConversionUom)
	} else {
		b.WriteString("Konversi: belum diatur (\"set 1 " + g.Name + " 15lt\")\n")
	}
	if inv, ierr := a.invRepo.WithTx(a.db).GetByChatGoods(ctx, msg.ChatID, g.ID); ierr == nil {
		fmt.Fprintf(&b, "Stok    : %g %s\n", inv.StockQty, inv.Unit)
	}
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, b.String(), intentCost)
}

// handleSetFactor menyimpan faktor konversi barang (1 uom = factor
// conversionUom), mis. "set 1 galon 15lt". Menimpa faktor lama.
func (a *goodsAgent) handleSetFactor(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, intentCost float64) error {
	itemName, _ := params["item_name"].(string)
	factorQty, _ := params["factor_qty"].(float64)
	factorUnit, _ := params["factor_unit"].(string)
	factorUnit = strings.ToLower(strings.TrimSpace(factorUnit))

	if itemName == "" || factorQty <= 0 || factorUnit == "" {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			"Format: \"set 1 [barang] [angka][satuan]\" (contoh: set 1 galon 15lt)", intentCost)
	}

	g, stop, err := a.resolveGood(ctx, msg, params, itemName, intentCost)
	if stop {
		return err
	}

	if err := a.goodsRepo.WithTx(a.db).UpdateConversion(ctx, g.ID, factorUnit, factorQty); err != nil {
		a.log.ErrorContext(ctx, "gagal simpan faktor konversi", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal menyimpan konversi.", intentCost)
	}

	a.log.InfoContext(ctx, "faktor konversi di-set user", "item", g.Name, "factor", factorQty, "unit", factorUnit)
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
		fmt.Sprintf("✅ Konversi %s disimpan: 1 %s = %g %s", g.Name, uomOrDefault(g.Uom), factorQty, factorUnit), intentCost)
}

// handleSetUom mengubah satuan kanonik barang, mis. "set satuan beras jadi kg".
func (a *goodsAgent) handleSetUom(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, intentCost float64) error {
	itemName, _ := params["item_name"].(string)
	unit, _ := params["unit"].(string)
	unit = strings.ToLower(strings.TrimSpace(unit))

	if itemName == "" || unit == "" {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			"Format: \"set satuan [barang] jadi [satuan]\" (contoh: set satuan beras jadi kg)", intentCost)
	}

	g, stop, err := a.resolveGood(ctx, msg, params, itemName, intentCost)
	if stop {
		return err
	}

	if err := a.goodsRepo.WithTx(a.db).UpdateUom(ctx, g.ID, unit); err != nil {
		a.log.ErrorContext(ctx, "gagal ubah satuan", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengubah satuan.", intentCost)
	}

	a.log.InfoContext(ctx, "satuan kanonik diubah", "item", g.Name, "uom", unit)
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
		fmt.Sprintf("✅ Satuan %s diubah ke %s.", g.Name, unit), intentCost)
}

// resolveGood mencocokkan nama barang ke master goods chat: exact → ILIKE
// search → saring via pesan asli. Bila ambigu, daftar kandidat bernomor dan
// daftarkan pending — jawaban "1"/"2" di-resume tanpa LLM hop.
// Return (goods, true, replyErr) bila permintaan sudah terbalas (stop).
func (a *goodsAgent) resolveGood(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, itemName string, intentCost float64) (*domain.Good, bool, error) {
	if g, err := a.goodsRepo.WithTx(a.db).GetByName(ctx, msg.ChatID, itemName); err == nil {
		return g, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		a.log.ErrorContext(ctx, "gagal lookup goods", "err", err)
		return nil, true, agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mencari barang.", intentCost)
	}

	results, err := a.goodsRepo.WithTx(a.db).SearchByName(ctx, msg.ChatID, itemName, 5)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal search goods", "err", err)
		return nil, true, agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mencari barang.", intentCost)
	}
	switch len(results) {
	case 0:
		return nil, true, agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			fmt.Sprintf("Barang '%s' belum ada di master. Barang otomatis tercatat saat dibeli.", itemName), intentCost)
	case 1:
		return &results[0], false, nil
	}

	// Beberapa kandidat: saring yang namanya muncul di pesan asli.
	lower := strings.ToLower(msg.Text)
	var matched []domain.Good
	for i := range results {
		if strings.Contains(lower, strings.ToLower(results[i].Name)) {
			matched = append(matched, results[i])
		}
	}
	if len(matched) == 1 {
		return &matched[0], false, nil
	}
	candidates := matched
	if len(candidates) == 0 {
		candidates = results
	}

	options := make([]string, len(candidates))
	for i, g := range candidates {
		options[i] = g.Name
	}
	if a.pending != nil {
		pendingParams := make(map[string]interface{}, len(params))
		for k, v := range params {
			pendingParams[k] = v
		}
		a.pending.Set(msg.ChatID, agent.PendingChoice{
			Action:       domain.ActionGoods,
			Params:       pendingParams,
			OptionKey:    "item_name",
			Options:      options,
			OriginalText: msg.Text,
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🔍 \"%s\" ketemu beberapa barang — pilih nomornya ya:\n", itemName)
	for i, g := range candidates {
		fmt.Fprintf(&b, "%d. %s (%s)\n", i+1, g.Name, uomOrDefault(g.Uom))
	}
	fmt.Fprintf(&b, "\nBalas nomornya (1-%d).", len(candidates))
	return nil, true, agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, b.String(), intentCost)
}

// uomOrDefault mengembalikan satuan kanonik barang, atau "pcs" bila belum
// diatur (barang auto-create dari ekstraksi LLM menyimpan satuan beli).
func uomOrDefault(uom string) string {
	if strings.TrimSpace(uom) == "" {
		return "pcs"
	}
	return uom
}

// categoryOrDefault mengembalikan kategori kanonik barang, atau "(belum
// diatur)" bila masih kosong.
func categoryOrDefault(category string) string {
	if strings.TrimSpace(category) == "" {
		return "(belum diatur)"
	}
	return category
}
