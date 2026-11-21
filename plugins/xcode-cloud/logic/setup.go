package main

import (
	"github.com/JustinGuedes/enplace-plugins/plugins/xcode-cloud/cloud"
)

// setUp makes Enplace's workflow, copied from the Default workflow Xcode
// made, or puts it right when it's there.
func (r *run) setUp() {
	r.refresh(nil)
	s := r.pending()
	switch {
	case s == nil || s.ProductID == "":
		r.res.Toast = "Connect Xcode Cloud once in Xcode first: Product → Xcode Cloud → Create Workflow."
		return
	case s.Own != "":
		r.res.Toast = "Enplace's workflow is set up and up to date."
		return
	}
	c, err := r.client()
	if err == nil {
		err = r.make(c, s)
	}
	if err != nil {
		s.SetUpError = err.Error()
		r.res.SetProject(r.pid, keyStatus, s)
		r.publish(s)
		r.res.Toast = "Couldn't set it up: " + err.Error()
		return
	}
	s.SetUpError = ""
	r.res.SetProject(r.pid, keyStatus, s)
	r.publish(s)
	r.res.Toast = "Made the " + cloud.OwnName + " workflow in Xcode Cloud."
	r.res.Log = "Made the " + cloud.OwnName + " workflow in Xcode Cloud"
}

// pending is the status refresh just wrote, before it's stored.
func (r *run) pending() *Status {
	if r.res.State.Projects == nil {
		return nil
	}
	var s *Status
	st := r.res.State
	st.GetProject(r.pid, keyStatus, &s)
	return s
}

func (r *run) client() (*cloud.Client, error) {
	return cloud.New(call, r.req.Secrets["asc_issuer_id"], r.req.Secrets["asc_key_id"], r.req.Secrets["asc_private_key"], r.req.Now)
}

// make creates Enplace's workflow from the Default one.
func (r *run) make(c *cloud.Client, s *Status) error {
	src, ok := cloud.Source(s.Workflows)
	if !ok {
		return errNoDefault
	}
	from, err := c.Workflow(src.ID)
	if err != nil {
		return err
	}
	want, err := cloud.Want(from, r.audience())
	if err != nil {
		return err
	}
	id, err := c.Make(s.ProductID, want)
	if err != nil {
		return err
	}
	s.Own, s.Branch = id, from.Branch
	s.Workflows = append(s.Workflows, cloud.Workflow{ID: id, Name: cloud.OwnName, Enabled: true})
	return nil
}

// keep puts Enplace's workflow back when it was changed in Xcode, or when
// "Builds it makes" changed.
func (r *run) keep(c *cloud.Client, s *Status, own cloud.Workflow) {
	have, err := c.Workflow(own.ID)
	if err != nil {
		return
	}
	want, err := cloud.Want(have, r.audience())
	if err != nil || cloud.Same(have, want) {
		return
	}
	if err := c.Fix(own.ID, want); err != nil {
		s.Error = "Couldn't put the " + cloud.OwnName + " workflow back: " + err.Error()
		return
	}
	r.res.Log = "Put the " + cloud.OwnName + " workflow back the way Enplace keeps it"
}

type setUpError string

func (e setUpError) Error() string { return string(e) }

const errNoDefault = setUpError("Xcode Cloud has no workflow to copy yet: make the Default one in Xcode (Product → Xcode Cloud → Create Workflow)")
