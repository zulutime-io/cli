package api

import (
	"bytes"
	"encoding/json"
	"strings"
)

type CommitRef struct {
	SHA     string `json:"sha,omitempty"`
	Subject string `json:"subject,omitempty"`
}

type CommitList []CommitRef

func (c *CommitList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*c = nil
		return nil
	}
	if len(data) > 1 && data[0] == '[' {
		rest := bytes.TrimSpace(data[1:])
		if len(rest) > 0 && rest[0] == '"' {
			var strs []string
			if err := json.Unmarshal(data, &strs); err != nil {
				return err
			}
			out := make(CommitList, 0, len(strs))
			for _, s := range strs {
				out = append(out, CommitRef{Subject: s})
			}
			*c = out
			return nil
		}
	}
	var objs []CommitRef
	if err := json.Unmarshal(data, &objs); err != nil {
		return err
	}
	*c = objs
	return nil
}

func (c CommitList) Subjects() []string {
	var out []string
	for _, x := range c {
		if x.Subject != "" {
			out = append(out, x.Subject)
		} else if x.SHA != "" {
			out = append(out, x.SHA)
		}
	}
	return out
}

func (c CommitList) JoinSubjects(sep string) string {
	return strings.Join(c.Subjects(), sep)
}

// CollectBookedIndexes builds sha/subject sets from entries' source_meta.
func CollectBookedIndexes(entries []TimeEntry) (shas map[string]bool, subjects map[string]bool) {
	shas = map[string]bool{}
	subjects = map[string]bool{}
	for _, e := range entries {
		if e.SourceMeta == nil {
			continue
		}
		for _, c := range e.SourceMeta.Commits {
			if c.SHA != "" {
				shas[c.SHA] = true
			}
			if c.Subject != "" {
				subjects[c.Subject] = true
			}
		}
	}
	return shas, subjects
}

func MergeCommitLists(lists ...CommitList) CommitList {
	seen := map[string]bool{}
	var out CommitList
	for _, list := range lists {
		for _, c := range list {
			key := c.SHA
			if key == "" {
				key = "s:" + c.Subject
			}
			if key == "s:" || seen[key] {
				continue
			}
			// prefix-dedupe SHAs
			if c.SHA != "" {
				dup := false
				for s := range seen {
					if strings.HasPrefix(s, "s:") {
						continue
					}
					if strings.HasPrefix(c.SHA, s) || strings.HasPrefix(s, c.SHA) {
						dup = true
						break
					}
				}
				if dup {
					continue
				}
			}
			seen[key] = true
			out = append(out, c)
		}
	}
	return out
}
