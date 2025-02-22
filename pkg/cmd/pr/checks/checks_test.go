package checks

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/browser"
	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/internal/run"
	"github.com/cli/cli/v2/pkg/cmd/pr/shared"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdChecks(t *testing.T) {
	tests := []struct {
		name       string
		cli        string
		wants      ChecksOptions
		wantsError string
	}{
		{
			name: "no arguments",
			cli:  "",
			wants: ChecksOptions{
				Interval: time.Duration(10000000000),
			},
		},
		{
			name: "pr argument",
			cli:  "1234",
			wants: ChecksOptions{
				SelectorArg: "1234",
				Interval:    time.Duration(10000000000),
			},
		},
		{
			name: "watch flag",
			cli:  "--watch",
			wants: ChecksOptions{
				Watch:    true,
				Interval: time.Duration(10000000000),
			},
		},
		{
			name: "watch flag and interval flag",
			cli:  "--watch --interval 5",
			wants: ChecksOptions{
				Watch:    true,
				Interval: time.Duration(5000000000),
			},
		},
		{
			name:       "interval flag without watch flag",
			cli:        "--interval 5",
			wantsError: "cannot use `--interval` flag without `--watch` flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, _ := iostreams.Test()
			f := &cmdutil.Factory{
				IOStreams: ios,
			}

			argv, err := shlex.Split(tt.cli)
			assert.NoError(t, err)

			var gotOpts *ChecksOptions
			cmd := NewCmdChecks(f, func(opts *ChecksOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantsError != "" {
				assert.EqualError(t, err, tt.wantsError)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wants.SelectorArg, gotOpts.SelectorArg)
			assert.Equal(t, tt.wants.Watch, gotOpts.Watch)
			assert.Equal(t, tt.wants.Interval, gotOpts.Interval)
		})
	}
}

func Test_checksRun(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		prJSON  string
		tty     bool
		watch   bool
		wantOut string
		wantErr string
	}{
		{
			name:    "no commits",
			prJSON:  `{ "number": 123 }`,
			tty:     true,
			wantOut: "",
			wantErr: "no commit found on the pull request",
		},
		{
			name:    "no checks",
			prJSON:  `{ "number": 123, "statusCheckRollup": { "nodes": [{"commit": {"oid": "abc"}}]}, "headRefName": "master" }`,
			tty:     true,
			wantOut: "",
			wantErr: "no checks reported on the 'master' branch",
		},
		{
			name:    "some failing",
			fixture: "./fixtures/someFailing.json",
			tty:     true,
			wantOut: "Some checks were not successful\n1 failing, 1 successful, 0 skipped, and 1 pending checks\n\nX  sad tests   1m26s  sweet link\n✓  cool tests  1m26s  sweet link\n*  slow tests  1m26s  sweet link\n",
			wantErr: "SilentError",
		},
		{
			name:    "some pending",
			fixture: "./fixtures/somePending.json",
			tty:     true,
			wantOut: "Some checks are still pending\n0 failing, 2 successful, 0 skipped, and 1 pending checks\n\n✓  cool tests  1m26s  sweet link\n✓  rad tests   1m26s  sweet link\n*  slow tests  1m26s  sweet link\n",
			wantErr: "SilentError",
		},
		{
			name:    "all passing",
			fixture: "./fixtures/allPassing.json",
			tty:     true,
			wantOut: "All checks were successful\n0 failing, 3 successful, 0 skipped, and 0 pending checks\n\n✓  awesome tests  1m26s  sweet link\n✓  cool tests     1m26s  sweet link\n✓  rad tests      1m26s  sweet link\n",
			wantErr: "",
		},
		{
			name:    "watch all passing",
			fixture: "./fixtures/allPassing.json",
			tty:     true,
			watch:   true,
			wantOut: "\x1b[?1049hAll checks were successful\n0 failing, 3 successful, 0 skipped, and 0 pending checks\n\n✓  awesome tests  1m26s  sweet link\n✓  cool tests     1m26s  sweet link\n✓  rad tests      1m26s  sweet link\n\x1b[?1049lAll checks were successful\n0 failing, 3 successful, 0 skipped, and 0 pending checks\n\n✓  awesome tests  1m26s  sweet link\n✓  cool tests     1m26s  sweet link\n✓  rad tests      1m26s  sweet link\n",
			wantErr: "",
		},
		{
			name:    "with statuses",
			fixture: "./fixtures/withStatuses.json",
			tty:     true,
			wantOut: "Some checks were not successful\n1 failing, 2 successful, 0 skipped, and 0 pending checks\n\nX  a status           sweet link\n✓  cool tests  1m26s  sweet link\n✓  rad tests   1m26s  sweet link\n",
			wantErr: "SilentError",
		},
		{
			name:    "no checks",
			prJSON:  `{ "number": 123, "statusCheckRollup": { "nodes": [{"commit": {"oid": "abc"}}]}, "headRefName": "master" }`,
			wantOut: "",
			wantErr: "no checks reported on the 'master' branch",
		},
		{
			name:    "some failing",
			fixture: "./fixtures/someFailing.json",
			wantOut: "sad tests\tfail\t1m26s\tsweet link\ncool tests\tpass\t1m26s\tsweet link\nslow tests\tpending\t1m26s\tsweet link\n",
			wantErr: "SilentError",
		},
		{
			name:    "some pending",
			fixture: "./fixtures/somePending.json",
			wantOut: "cool tests\tpass\t1m26s\tsweet link\nrad tests\tpass\t1m26s\tsweet link\nslow tests\tpending\t1m26s\tsweet link\n",
			wantErr: "SilentError",
		},
		{
			name:    "all passing",
			fixture: "./fixtures/allPassing.json",
			wantOut: "awesome tests\tpass\t1m26s\tsweet link\ncool tests\tpass\t1m26s\tsweet link\nrad tests\tpass\t1m26s\tsweet link\n",
			wantErr: "",
		},
		{
			name:    "with statuses",
			fixture: "./fixtures/withStatuses.json",
			wantOut: "a status\tfail\t0\tsweet link\ncool tests\tpass\t1m26s\tsweet link\nrad tests\tpass\t1m26s\tsweet link\n",
			wantErr: "SilentError",
		},
		{
			name:    "some skipped",
			fixture: "./fixtures/someSkipping.json",
			tty:     true,
			wantOut: "All checks were successful\n0 failing, 1 successful, 2 skipped, and 0 pending checks\n\n✓  cool tests  1m26s  sweet link\n-  rad tests   1m26s  sweet link\n-  skip tests  1m26s  sweet link\n",
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, _ := iostreams.Test()
			ios.SetStdoutTTY(tt.tty)
			ios.SetAlternateScreenBufferEnabled(tt.tty)

			var response *api.PullRequest
			var jsonReader io.Reader
			if tt.fixture != "" {
				ff, err := os.Open(tt.fixture)
				require.NoError(t, err)
				defer ff.Close()
				jsonReader = ff
			} else {
				jsonReader = bytes.NewBufferString(tt.prJSON)
			}
			dec := json.NewDecoder(jsonReader)
			require.NoError(t, dec.Decode(&response))

			opts := &ChecksOptions{
				IO:          ios,
				SelectorArg: "123",
				Finder:      shared.NewMockFinder("123", response, ghrepo.New("OWNER", "REPO")),
				Watch:       tt.watch,
			}

			err := checksRun(opts)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.wantOut, stdout.String())
		})
	}
}

func TestChecksRun_web(t *testing.T) {
	tests := []struct {
		name       string
		isTTY      bool
		wantStderr string
		wantStdout string
		wantBrowse string
	}{
		{
			name:       "tty",
			isTTY:      true,
			wantStderr: "Opening github.com/OWNER/REPO/pull/123/checks in your browser.\n",
			wantStdout: "",
			wantBrowse: "https://github.com/OWNER/REPO/pull/123/checks",
		},
		{
			name:       "nontty",
			isTTY:      false,
			wantStderr: "",
			wantStdout: "",
			wantBrowse: "https://github.com/OWNER/REPO/pull/123/checks",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			browser := &browser.Stub{}

			ios, _, stdout, stderr := iostreams.Test()
			ios.SetStdoutTTY(tc.isTTY)
			ios.SetStdinTTY(tc.isTTY)
			ios.SetStderrTTY(tc.isTTY)

			_, teardown := run.Stub()
			defer teardown(t)

			err := checksRunWebMode(&ChecksOptions{
				IO:          ios,
				Browser:     browser,
				WebMode:     true,
				SelectorArg: "123",
				Finder:      shared.NewMockFinder("123", &api.PullRequest{Number: 123}, ghrepo.New("OWNER", "REPO")),
			})
			assert.NoError(t, err)
			assert.Equal(t, tc.wantStdout, stdout.String())
			assert.Equal(t, tc.wantStderr, stderr.String())
			browser.Verify(t, tc.wantBrowse)
		})
	}
}

func TestEliminateDupulicates(t *testing.T) {
	tests := []struct {
		name          string
		checkContexts []api.CheckContext
		want          []api.CheckContext
	}{
		{
			name: "duplicate CheckRun (lint)",
			checkContexts: []api.CheckContext{
				{
					TypeName:    "CheckRun",
					Name:        "build (ubuntu-latest)",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "https://github.com/cli/cli/runs/1",
				},
				{
					TypeName:    "CheckRun",
					Name:        "lint",
					Status:      "COMPLETED",
					Conclusion:  "FAILURE",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "https://github.com/cli/cli/runs/2",
				},
				{
					TypeName:    "CheckRun",
					Name:        "lint",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					StartedAt:   time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					CompletedAt: time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					DetailsURL:  "https://github.com/cli/cli/runs/3",
				},
			},
			want: []api.CheckContext{
				{
					TypeName:    "CheckRun",
					Name:        "lint",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					StartedAt:   time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					CompletedAt: time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					DetailsURL:  "https://github.com/cli/cli/runs/3",
				},
				{
					TypeName:    "CheckRun",
					Name:        "build (ubuntu-latest)",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "https://github.com/cli/cli/runs/1",
				},
			},
		},
		{
			name: "duplicate StatusContext (Windows GPU)",
			checkContexts: []api.CheckContext{
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Windows GPU",
					State:       "FAILURE",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/2",
				},
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Windows GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					CompletedAt: time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/3",
				},
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Linux GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/1",
				},
			},
			want: []api.CheckContext{
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Windows GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					CompletedAt: time.Date(2022, 2, 2, 2, 2, 2, 2, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/3",
				},
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Linux GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/1",
				},
			},
		},
		{
			name: "unique CheckContext",
			checkContexts: []api.CheckContext{
				{
					TypeName:    "CheckRun",
					Name:        "build (ubuntu-latest)",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "https://github.com/cli/cli/runs/1",
				},
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Windows GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/2",
				},
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Linux GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/3",
				},
			},
			want: []api.CheckContext{
				{
					TypeName:    "CheckRun",
					Name:        "build (ubuntu-latest)",
					Status:      "COMPLETED",
					Conclusion:  "SUCCESS",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "https://github.com/cli/cli/runs/1",
				},
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Windows GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/2",
				},
				{
					TypeName:    "StatusContext",
					Name:        "",
					Context:     "Linux GPU",
					State:       "SUCCESS",
					Status:      "",
					Conclusion:  "",
					StartedAt:   time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					CompletedAt: time.Date(2022, 1, 1, 1, 1, 1, 1, time.UTC),
					DetailsURL:  "",
					TargetURL:   "https://github.com/cli/cli/3",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eliminateDuplicates(tt.checkContexts)
			if !reflect.DeepEqual(tt.want, got) {
				t.Errorf("got eliminateDuplicates %+v, want %+v\n", got, tt.want)
			}
		})
	}
}
