package edit

import (
	"errors"
	"strings"

	"github.com/elves/elvish/eval"
	"github.com/elves/elvish/eval/types"
	"github.com/elves/elvish/eval/vartypes"
	"github.com/elves/elvish/util"
	"github.com/xiaq/persistent/hashmap"
)

var (
	errIncorrectNumOfResults    = errors.New("matcher must return a bool for each candidate")
	errMatcherMustBeFn          = errors.New("matcher must be a function")
	errMatcherInputMustBeString = errors.New("matcher input must be string")
)

var (
	matchPrefix = &eval.BuiltinFn{
		"edit:match-prefix", wrapMatcher(strings.HasPrefix)}
	matchSubstr = &eval.BuiltinFn{
		"edit:match-substr", wrapMatcher(strings.Contains)}
	matchSubseq = &eval.BuiltinFn{
		"edit:match-subseq", wrapMatcher(util.HasSubseq)}
	matchers = []*eval.BuiltinFn{
		matchPrefix,
		matchSubstr,
		matchSubseq,
	}

	_ = RegisterVariable("-matcher", func() vartypes.Variable {
		m := hashmap.Empty.Assoc(
			// Fallback matcher uses empty string as key
			types.String(""), matchPrefix)
		return vartypes.NewValidatedPtrVariable(types.NewMap(m), eval.ShouldBeMap)
	})
)

func (ed *Editor) lookupMatcher(name string) (eval.Fn, bool) {
	m := ed.variables["-matcher"].Get().(types.Map)
	if !m.HasKey(types.String(name)) {
		// Use fallback matcher
		name = ""
	}
	matcher, ok := m.IndexOne(types.String(name)).(eval.Fn)
	return matcher, ok
}

func wrapMatcher(matcher func(s, p string) bool) eval.BuiltinFnImpl {
	return func(ec *eval.Frame,
		args []types.Value, opts map[string]types.Value) {

		var pattern types.String
		iterate := eval.ScanArgsOptionalInput(ec, args, &pattern)
		var options struct {
			IgnoreCase bool
			SmartCase  bool
		}
		eval.ScanOptsToStruct(opts, &options)
		switch {
		case options.IgnoreCase && options.SmartCase:
			throwf("-ignore-case and -smart-case cannot be used together")
		case options.IgnoreCase:
			innerMatcher := matcher
			matcher = func(s, p string) bool {
				return innerMatcher(strings.ToLower(s), strings.ToLower(p))
			}
		case options.SmartCase:
			innerMatcher := matcher
			matcher = func(s, p string) bool {
				if p == strings.ToLower(p) {
					// Ignore case is pattern is all lower case.
					return innerMatcher(strings.ToLower(s), p)
				} else {
					return innerMatcher(s, p)
				}
			}
		}

		out := ec.OutputChan()
		iterate(func(v types.Value) {
			s, ok := v.(types.String)
			if !ok {
				throw(errMatcherInputMustBeString)
			}
			out <- types.Bool(matcher(string(s), string(pattern)))
		})
	}
}
