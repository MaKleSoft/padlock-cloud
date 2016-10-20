package padlockcloud

import fp "path/filepath"
import t "html/template"
import "errors"

// Wrapper for holding references to template instances used for rendering emails, webpages etc.
type Templates struct {
	BasePage  *t.Template
	BaseEmail *t.Template
	// Email template for api key activation email
	ActivateAuthTokenEmail *t.Template
	// Template for success page for activating an auth token
	ActivateAuthTokenSuccess *t.Template
	// Email template for clients using an outdated api version
	DeprecatedVersionEmail *t.Template
	ErrorPage              *t.Template
	LoginPage              *t.Template
	Dashboard              *t.Template
	DeleteStore            *t.Template
}

func ExtendTemplate(base *t.Template, path string) (*t.Template, error) {
	if base == nil {
		return nil, errors.New("Base page is nil")
	}

	b, err := base.Clone()
	if err != nil {
		return nil, err
	}

	return b.ParseFiles(path)
}

// Loads templates from given directory
func LoadTemplates(tt *Templates, p string) error {
	var err error

	if tt.BaseEmail, err = t.ParseFiles(fp.Join(p, "email/base.txt")); err != nil {
		return err
	}
	if tt.BasePage, err = t.ParseFiles(fp.Join(p, "page/base.html")); err != nil {
		return err
	}
	if tt.ActivateAuthTokenSuccess, err = ExtendTemplate(tt.BasePage, fp.Join(p, "page/activate-auth-token-success.html")); err != nil {
		return err
	}
	if tt.ActivateAuthTokenEmail, err = ExtendTemplate(tt.BaseEmail, fp.Join(p, "email/activate-auth-token.txt")); err != nil {
		return err
	}
	if tt.DeprecatedVersionEmail, err = ExtendTemplate(tt.BaseEmail, fp.Join(p, "email/deprecated-version.txt")); err != nil {
		return err
	}
	if tt.ActivateAuthTokenSuccess, err = ExtendTemplate(tt.BasePage, fp.Join(p, "page/activate-auth-token-success.html")); err != nil {
		return err
	}
	if tt.ErrorPage, err = ExtendTemplate(tt.BasePage, fp.Join(p, "page/error.html")); err != nil {
		return err
	}
	if tt.LoginPage, err = ExtendTemplate(tt.BasePage, fp.Join(p, "page/login.html")); err != nil {
		return err
	}
	if tt.Dashboard, err = ExtendTemplate(tt.BasePage, fp.Join(p, "page/dashboard.html")); err != nil {
		return err
	}
	if tt.DeleteStore, err = ExtendTemplate(tt.BasePage, fp.Join(p, "page/delete-store.html")); err != nil {
		return err
	}

	return nil
}
