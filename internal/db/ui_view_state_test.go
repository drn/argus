package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestLoadHideHeraManaged_AbsentReturnsFalse(t *testing.T) {
	d := testDB(t)
	got, err := d.LoadHideHeraManaged()
	testutil.NoError(t, err)
	testutil.Equal(t, got, false)
}

func TestHideHeraManaged_RoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  bool
	}{
		{"true", true},
		{"false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDB(t)
			testutil.NoError(t, d.SaveHideHeraManaged(tc.val))
			got, err := d.LoadHideHeraManaged()
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc.val)
		})
	}
}

func TestHideHeraManaged_RoundTripOverwrite(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SaveHideHeraManaged(true))
	got, err := d.LoadHideHeraManaged()
	testutil.NoError(t, err)
	testutil.Equal(t, got, true)

	testutil.NoError(t, d.SaveHideHeraManaged(false))
	got, err = d.LoadHideHeraManaged()
	testutil.NoError(t, err)
	testutil.Equal(t, got, false)
}

func TestLoadLastTab_AbsentReturnsEmpty(t *testing.T) {
	d := testDB(t)
	got, err := d.LoadLastTab()
	testutil.NoError(t, err)
	testutil.Equal(t, got, "")
}

func TestLastTab_RoundTrip(t *testing.T) {
	for _, tc := range []string{"tasks", "hera", "settings"} {
		t.Run(tc, func(t *testing.T) {
			d := testDB(t)
			testutil.NoError(t, d.SaveLastTab(tc))
			got, err := d.LoadLastTab()
			testutil.NoError(t, err)
			testutil.Equal(t, got, tc)
		})
	}
}

func TestLastTab_RoundTripOverwrite(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SaveLastTab("hera"))
	got, err := d.LoadLastTab()
	testutil.NoError(t, err)
	testutil.Equal(t, got, "hera")

	testutil.NoError(t, d.SaveLastTab("settings"))
	got, err = d.LoadLastTab()
	testutil.NoError(t, err)
	testutil.Equal(t, got, "settings")
}
