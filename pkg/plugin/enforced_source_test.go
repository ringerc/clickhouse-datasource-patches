package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticValueSource(t *testing.T) {
	t.Run("Resolve always returns the configured value", func(t *testing.T) {
		src := staticValueSource{value: "tenant_a"}
		v, ok, err := src.Resolve(context.Background())
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "tenant_a", v)
	})

	t.Run("Kind is static", func(t *testing.T) {
		src := staticValueSource{value: "x"}
		assert.Equal(t, customSettingSourceStatic, src.Kind())
	})

	t.Run("empty value is returned as-is", func(t *testing.T) {
		src := staticValueSource{value: ""}
		v, ok, err := src.Resolve(context.Background())
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "", v)
	})
}

func TestHeaderValueSource(t *testing.T) {
	t.Run("Kind is header", func(t *testing.T) {
		src := headerValueSource{headerName: "X-Tenant"}
		assert.Equal(t, customSettingSourceHeader, src.Kind())
	})

	t.Run("Resolve returns value from ctx headers map", func(t *testing.T) {
		src := headerValueSource{headerName: "X-Tenant"}
		ctx := WithForwardedHeaders(context.Background(), map[string]string{
			"X-Tenant": "tenant_a",
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "tenant_a", v)
	})

	t.Run("Resolve returns false when header absent", func(t *testing.T) {
		src := headerValueSource{headerName: "X-Tenant"}
		ctx := WithForwardedHeaders(context.Background(), map[string]string{
			"X-Other": "something",
		})
		_, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("Resolve returns false with nil headers map", func(t *testing.T) {
		src := headerValueSource{headerName: "X-Tenant"}
		_, ok, err := src.Resolve(context.Background())
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("canonical key lookup is case-sensitive after canonicalisation", func(t *testing.T) {
		// headerName is already canonicalised by factory; map keys must match.
		src := headerValueSource{headerName: "X-Allowed-Projects"}
		ctx := WithForwardedHeaders(context.Background(), map[string]string{
			"X-Allowed-Projects": "prj_a",
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "prj_a", v)
	})

	t.Run("present-but-empty header value is returned as ok=true", func(t *testing.T) {
		src := headerValueSource{headerName: "X-Tenant"}
		ctx := WithForwardedHeaders(context.Background(), map[string]string{
			"X-Tenant": "",
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "", v)
	})
}

func TestBuildEnforcedBinding(t *testing.T) {
	t.Run("static default with empty source", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:  "max_threads",
			Value:    "4",
			Enforced: true,
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		assert.Equal(t, "max_threads", b.Setting)
		assert.Equal(t, onMissingReject, b.OnMissing)
		assert.Equal(t, customSettingSourceStatic, b.Source.Kind())
	})

	t.Run("explicit static source", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:  "max_threads",
			Value:    "4",
			Enforced: true,
			Source:   customSettingSourceStatic,
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		assert.Equal(t, customSettingSourceStatic, b.Source.Kind())
	})

	t.Run("header source happy path", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:    "custom_tenant",
			Enforced:   true,
			Source:     customSettingSourceHeader,
			HeaderName: "X-Tenant",
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		assert.Equal(t, customSettingSourceHeader, b.Source.Kind())
		assert.Equal(t, onMissingReject, b.OnMissing)
	})

	t.Run("header source onMissing=empty accepted", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:    "custom_tenant",
			Enforced:   true,
			Source:     customSettingSourceHeader,
			HeaderName: "X-Tenant",
			OnMissing:  onMissingEmpty,
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		assert.Equal(t, onMissingEmpty, b.OnMissing)
	})

	t.Run("header source without headerName is rejected", func(t *testing.T) {
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:  "s1",
			Enforced: true,
			Source:   customSettingSourceHeader,
		}, EnforcedSourceRuntime{})
		assert.Error(t, err)
	})

	t.Run("header source with value set is rejected", func(t *testing.T) {
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:    "s1",
			Value:      "foo",
			Enforced:   true,
			Source:     customSettingSourceHeader,
			HeaderName: "X-Hdr",
		}, EnforcedSourceRuntime{})
		assert.Error(t, err)
	})

	t.Run("header source with enforced=false is rejected", func(t *testing.T) {
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:    "s1",
			Source:     customSettingSourceHeader,
			HeaderName: "X-Hdr",
		}, EnforcedSourceRuntime{})
		assert.Error(t, err)
	})

	t.Run("header source binding to readonly case-insensitive is rejected", func(t *testing.T) {
		for _, name := range []string{"readonly", "READONLY", "ReadOnly"} {
			name := name
			t.Run(name, func(t *testing.T) {
				_, err := BuildEnforcedBinding(CustomSetting{
					Setting:    name,
					Enforced:   true,
					Source:     customSettingSourceHeader,
					HeaderName: "X-Hdr",
				}, EnforcedSourceRuntime{})
				assert.Error(t, err)
			})
		}
	})

	t.Run("unknown source is rejected", func(t *testing.T) {
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:  "s1",
			Value:    "v",
			Enforced: true,
			Source:   "magic",
		}, EnforcedSourceRuntime{})
		assert.Error(t, err)
	})

	t.Run("headerName is canonicalised by factory", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:    "s1",
			Enforced:   true,
			Source:     customSettingSourceHeader,
			HeaderName: "x-allowed-projects",
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		hs, ok := b.Source.(headerValueSource)
		require.True(t, ok)
		assert.Equal(t, "X-Allowed-Projects", hs.headerName)
	})
}

func TestRegisterEnforcedValueSourceFactory(t *testing.T) {
	t.Run("panics on empty kind", func(t *testing.T) {
		assert.Panics(t, func() {
			RegisterEnforcedValueSourceFactory("", nil)
		})
	})

	t.Run("registration and lookup succeed", func(t *testing.T) {
		called := false
		RegisterEnforcedValueSourceFactory("test-source-xyz", func(cs CustomSetting, _ EnforcedSourceRuntime) (EnforcedValueSource, error) {
			called = true
			return staticValueSource{value: "test"}, nil
		})
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:  "s1",
			Enforced: true,
			Source:   "test-source-xyz",
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		assert.True(t, called)
	})
}
