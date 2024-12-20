package ctxexcludes

var Excludes = map[string]bool{
	".git":                        true,
	"dist":                        true,
	"node_modules":                true,
	".svn":                        true,
	".hg":                         true,
	".DS_Store":                   true,
	"__MACOSX":                    true,
	"__pycache__":                 true,
	".tox":                        true,
	".mypy_cache":                 true,
	".pytest_cache":               true,
	"Debug":                       true,
	"Release":                     true,
	".vs":                         true,
	".idea":                       true,
	"cmake-build-debug":           true,
	"target":                      true,
	".gradle":                     true,
	".classpath":                  true,
	".project":                    true,
	".bundle":                     true,
	"vendor/bundle":               true,
	"bin":                         true,
	"pkg":                         true,
	"docker-compose.override.yml": true,
	".dockerignore":               true,
	".vscode":                     true,
	".env":                        true,
	"logs":                        true,
	"coverage":                    true,
}
