# LLM-Based Routing Architecture (Refactoring Proposal)

## Konsep

Sistem yang **sederhana dan parameternya jelas** di mana LLM bertugas sebagai **adapter** dari natural language ke service API yang sudah ditentukan.

## Arsitektur Baru yang Diusulkan:

### ✅ **Service = Source of Truth**
```go
// Service API yang JELAS dan TETAP
type ServiceParams struct {
    // Untuk get_stock
    ItemFilter string `json:"item_filter,omitempty"`
    
    // Untuk get_report  
    ReportType string `json:"report_type,omitempty"` 
    Period     string `json:"period,omitempty"`
    ItemFilter string `json:"item_filter,omitempty"`
    
    // Untuk record_transaction
    TransactionData *Extraction `json:"transaction_data,omitempty"`
}
```

### ✅ **LLM = Translator/Adapter**
```go
// LLM bertugas mengubah natural language → structured params
User: "stok kecap tanggal 11/08"
  ↓
LLM: {"action": "get_stock", "params": {"item_filter": "kecap", "period": "custom", ...}}
  ↓
Service: GetStock(ServiceParams{ItemFilter: "kecap", ...})
```

## Kelebihan vs Arsitektur Saat Ini:

### **Saat Ini (Regex-heavy):**
```go
// 100+ regex patterns untuk deteksi intent
isReportQuery()     // 50+ markers
parseMetric()        // 10+ keywords  
parsePeriod()        // 8+ patterns
parseItemFilter()    // 3+ regex patterns
```

### **Diusulkan (LLM-centric):**
```go
// 1 LLM function untuk SEMUA intent classification
LLM Classify() → Intent + Parameters in one call
// Clean switch/case routing based on action
```

## Implementasi:

### 1. **ServiceAction Structure** (✅ Created)
```go
type ServiceAction struct {
    Action string                 `json:"action"`
    Params map[string]interface{} `json:"params"`
    Data   *Extraction             `json:"data,omitempty"`
}
```

### 2. **Action Types** (✅ Defined)
```go
const (
    ActionRecordTransaction = "record_transaction"
    ActionGetStock         = "get_stock"
    ActionGetReport         = "get_report"
    ActionInit              = "init"
    ActionHelp              = "help"
    ActionInfo              = "info"
    ActionNone              = "none"
)
```

### 3. **LLM Prompt for Intent Classification** (✅ Created)
- Comprehensive prompt for extracting action + parameters
- Support for all existing query patterns
- Better typo tolerance built-in
- Clean parameter extraction

## Contoh Perbandingan:

### **Query yang Beragam:**
```
"stok kecap"          → LLM: {action:"get_stock",params:{item_filter:"kecap"}}
"sisa air"            → LLM: {action:"get_stock",params:{item_filter:"air"}}
"persedian barang"     → LLM: {action:"get_stock",params:{}} // typo support
"ringkasan kemarin"   → LLM: {action:"get_report",params:{report_type:"summary",period:"yesterday"}}
```

### **Service Functions yang Simple:**
```go
func (s *Service) GetStock(params ServiceParams) Response
func (s *Service) GetReport(params ServiceParams) Response  
func (s *Service) RecordTransaction(tx Transaction) Response
```

## Status Saat Ini:

✅ **Konsep yang Telah Dibuat:**
- ServiceAction dan ServiceParams structures
- SystemPromptIntent untuk LLM intent classification  
- IntentExtractor interface definition
- Arsitektur baru didokumentasikan

❌ **Yang Perlu Implementasi:**
- LLM intent extractor implementation
- Refactor Agent.Process() untuk menggunakan LLM routing
- Handler functions untuk clean service API
- Remove 100+ regex patterns

## Benefits:

✅ **Scalable** - Tambah feature = update prompt, bukan regex  
✅ **Consistent** - Service API jelas, LLM adapt ke service  
✅ **Maintainable** - Single source of truth di service  
✅ **Flexible** - Natural language understanding delegated ke LLM  

Ini adalah **foundation** untuk arsitektur yang kamu minta - LLM sebagai translator untuk service yang simple dan parameternya jelas! 💡