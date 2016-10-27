package templates_test

import (
	"time"

	"git.topfreegames.com/topfreegames/marathon/models"
	"git.topfreegames.com/topfreegames/marathon/templates"
	mt "git.topfreegames.com/topfreegames/marathon/testing"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/uber-go/zap"
)

var _ = Describe("Cache", func() {
	var (
		l zap.Logger
	)
	BeforeEach(func() {
		l = mt.NewMockLogger()
	})
	Describe("FetchTemplate", func() {
		It("Should find a cached template", func() {
			tc := templates.CreateTemplateCache(1)
			template := &models.Template{
				Name:     "test_cached_template1",
				Locale:   "en",
				Defaults: map[string]interface{}{"param1": "value1", "param2": "value2"},
				Body:     map[string]interface{}{"alert": "{{value1}}, {{value2}}"},
			}

			cachedTplBeforeCache := tc.FindTemplate(l, "test_cached_template1", "gcm", "en")
			Expect(cachedTplBeforeCache).To(BeNil())

			tc.AddTemplate(l, "test_cached_template1", "gcm", "en", template)

			cachedTplAfterCache := tc.FindTemplate(l, "test_cached_template1", "gcm", "en")
			Expect(cachedTplAfterCache).NotTo(BeNil())

			time.Sleep(1 * time.Second)
			cachedTplAfterExpiredCache := tc.FindTemplate(l, "test_cached_template1", "gcm", "en")
			Expect(cachedTplAfterExpiredCache).To(BeNil())
		})
	})
})
