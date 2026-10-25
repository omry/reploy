package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestMappedPodmanCreateRequiresExactObjectBindingV1(t *testing.T) {
	for _, test := range []struct {
		name                    string
		change                  func(map[string]any)
		status                  int
		malformed, cleanupOwned bool
	}{
		{name: "exact", status: 201},
		{name: "wrong ID", status: 201, change: func(o map[string]any) { o["Id"] = strings.Repeat("c", 64) }},
		{name: "wrong name", status: 201, change: func(o map[string]any) { o["Name"] = "/foreign" }},
		{name: "wrong image", status: 201, change: func(o map[string]any) { o["Image"] = "sha256:" + strings.Repeat("c", 64) }},
		{name: "wrong attempt", status: 201, change: func(o map[string]any) {
			o["Config"] = map[string]any{"Labels": map[string]string{podmanApplicationAttemptLabel: "foreign"}}
		}},
		{name: "malformed inspect", status: 201, malformed: true},
		{name: "ambiguous 201 owned", status: 201, malformed: true, cleanupOwned: true},
		{name: "server failure owned", status: 500, cleanupOwned: true},
		{name: "name collision", status: 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := sameIDApplicationSpec(t)
			body, name, err := podmanApplicationCreateRequest(spec, "auto:size=1,uidmapping=0:1:1,gidmapping=0:1:1")
			if err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("b", 64)
			var submitted podmanApplicationCreate
			posts, gets, removals := 0, 0, 0
			client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					if r.URL.Path != "/v1.44/containers/create" || r.URL.Query().Get("name") != name {
						t.Error("wrong create route")
					}
					if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
						t.Error(err)
					}
					w.WriteHeader(test.status)
					if test.status == 201 {
						fmt.Fprintf(w, `{"Id":%q}`, id)
					} else {
						io.WriteString(w, "secret-request-environment")
					}
					return
				}
				gets++
				ownedRecovery := r.URL.Path == "/v1.44/containers/"+name+"/json" && test.cleanupOwned
				if test.malformed && !ownedRecovery {
					io.WriteString(w, "secret-request-environment")
					return
				}
				object := map[string]any{"Id": id, "Name": "/" + name, "Image": body.Image, "Config": map[string]any{"Labels": submitted.Labels}}
				if test.change != nil && !ownedRecovery {
					test.change(object)
				}
				if test.name == "wrong ID" && strings.Contains(r.URL.Path, name) {
					object["Config"] = map[string]any{"Labels": map[string]string{}}
				}
				json.NewEncoder(w).Encode(object)
			})
			client.privateErrors = true
			raw := func(s CommandSpec, _ RunOptions) error {
				removals++
				if !slices.Equal(s.Args, []string{"container", "rm", "--force", id}) {
					t.Errorf("unsafe cleanup %v", s.Args)
				}
				return nil
			}
			got, err := client.createApplication(t.Context(), spec, body, name, body.Image, raw)
			if test.name == "exact" {
				if err != nil || got != id || gets != 1 || removals != 0 {
					t.Fatalf("result %q %v gets=%d rm=%d", got, err, gets, removals)
				}
			} else if err == nil || got != "" || strings.Contains(err.Error(), "secret-request-environment") {
				t.Fatalf("unsafe result %q %v", got, err)
			}
			if posts != 1 || (removals == 1) != test.cleanupOwned {
				t.Fatalf("posts=%d cleanup=%d", posts, removals)
			}
			if test.status == 409 && gets != 0 {
				t.Fatal("looked up foreign name collision")
			}
			if submitted.HostConfig.UsernsMode != body.HostConfig.UsernsMode || submitted.User != "0:0" || !submitted.HostConfig.ReadonlyRootfs || !slices.Equal(submitted.HostConfig.CapDrop, []string{"ALL"}) {
				t.Fatal("mapped request lost namespace or sandbox policy")
			}
		})
	}
}

func TestPodmanCreateOutgoingLimitBeforeHTTPV1(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			spec := sameIDApplicationSpec(t)
			body, name, err := podmanApplicationCreateRequest(spec, "auto:size=1,uidmapping=0:1:1,gidmapping=0:1:1")
			if err != nil {
				t.Fatal(err)
			}
			body.Labels[podmanApplicationAttemptLabel] = strings.Repeat("0", 32)
			body.Env = []string{"PADDING="}
			base, _ := json.Marshal(body)
			body.Env[0] += strings.Repeat("x", int(imageHealthConfigLimit)-len(base)+extra)
			delete(body.Labels, podmanApplicationAttemptLabel)
			calls := 0
			client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost {
					t.Error("unexpected inspect")
				}
				data, _ := io.ReadAll(r.Body)
				if int64(len(data)) != imageHealthConfigLimit {
					t.Errorf("request size %d", len(data))
				}
				w.WriteHeader(201)
				fmt.Fprintf(w, `{"Id":%q}`, strings.Repeat("b", 64))
			})
			_, err = client.createApplication(t.Context(), spec, body, name, "", nil)
			if extra == 0 && (err != nil || calls != 1) {
				t.Fatalf("exact bound: %v calls=%d", err, calls)
			}
			if extra == 1 && (err == nil || calls != 0) {
				t.Fatalf("oversize reached HTTP: %v calls=%d", err, calls)
			}
		})
	}
}

func TestMappedPodmanCreateNilAndCancelledV1(t *testing.T) {
	var p *preparedMappedPodmanCreateV1
	if _, err := p.create(t.Context(), nil); err == nil {
		t.Fatal("nil preparation accepted")
	}
	if _, _, err := p.observe(t.Context()); err == nil {
		t.Fatal("nil preparation observed")
	}
	if _, _, err := (&preparedMappedPodmanCreateV1{}).observe(nil); err == nil {
		t.Fatal("nil context observed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := (&preparedMappedPodmanCreateV1{}).observe(ctx); err == nil {
		t.Fatal("cancelled unowned preparation accepted")
	}
}

func TestMappedPodmanCreateRejectsOversizeResponseV1(t *testing.T) {
	spec := sameIDApplicationSpec(t)
	body, name, err := podmanApplicationCreateRequest(spec, "auto:size=1,uidmapping=0:1:1,gidmapping=0:1:1")
	if err != nil {
		t.Fatal(err)
	}
	posts, lookups := 0, 0
	client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"Id":%q,"Warnings":[%q]}`, strings.Repeat("b", 64), strings.Repeat("x", 64<<10))
			return
		}
		lookups++
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "private-environment-value")
	})
	client.privateErrors = true
	id, err := client.createApplication(t.Context(), spec, body, name, body.Image, func(CommandSpec, RunOptions) error { t.Error("unproved cleanup invoked"); return nil })
	if id != "" || err == nil || posts != 1 || lookups != 1 || strings.Contains(err.Error(), "private-environment-value") {
		t.Fatalf("oversize response accepted or unsafe recovery: %q %v POSTs=%d lookups=%d", id, err, posts, lookups)
	}
}
