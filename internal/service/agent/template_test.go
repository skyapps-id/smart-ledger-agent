package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHelpMessage(t *testing.T) {
	assert.Equal(t, OnboardingTemplate, HelpMessage(""))
	assert.Equal(t, OnboardingTemplate, HelpMessage("tidak ada"), "topik asing → panduan utama")
	assert.Equal(t, HelpTopics["stok"], HelpMessage(" Stok "), "topik case-insensitive & di-trim")

	// Panduan utama harus menunjuk ke setiap topik yang tersedia.
	for topic := range HelpTopics {
		assert.True(t, strings.Contains(OnboardingTemplate, "*bantuan "+topic+"*"), "topik %q tidak disebut di panduan utama", topic)
	}
}
