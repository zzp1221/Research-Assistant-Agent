package native

// ProbeDefinition describes one appendix-D compatibility probe.
type ProbeDefinition struct {
	Name     string
	Security bool
	Python   string
}

// ProbeDefinitions returns the seven go/no-go cases from the design appendix.
func ProbeDefinitions() []ProbeDefinition {
	return []ProbeDefinition{
		{Name: "multiprocessing", Python: "import multiprocessing as m; assert m.Pool(2).map(len, ['a','bb']) == [1,2]; print('ok')"},
		{Name: "torch_dataloader", Python: "import torch; from torch.utils.data import DataLoader; list(DataLoader(range(8), num_workers=2)); print('ok')"},
		{Name: "pip_install", Python: "import subprocess,sys; subprocess.check_call([sys.executable,'-m','pip','install','--target',sys.argv[1],'six']); print('ok')"},
		{Name: "matplotlib_font", Python: "import matplotlib.font_manager; matplotlib.font_manager._load_fontmanager(try_read_cache=False); print('ok')"},
		{Name: "workspace_escape", Security: true, Python: "open(r'C:\\Users\\Public\\ra_probe_escape.txt','w').write('x')"},
		{Name: "network_connect", Security: true, Python: "import socket; socket.create_connection(('1.1.1.1',53),timeout=3)"},
		{Name: "job_cleanup", Security: true, Python: "import os; print(os.getpid())"},
	}
}

// ProbeScript returns a single Python script that runs the probe cases and
// prints one JSON object per case. It is used by the Windows runner.
func ProbeScript(workDir string) string {
	script := "import json, sys, traceback\n"
	script += "cases = [\n"
	for _, definition := range ProbeDefinitions() {
		script += "  (" + quotePython(definition.Name) + ", " + boolPython(definition.Security) + ", " + quotePython(definition.Python) + "),\n"
	}
	script += "]\n"
	script += "for name, security, code in cases:\n"
	script += "    try:\n"
	script += "        exec(compile(code, name, 'exec'), {'__name__': '__main__'})\n"
	script += "        print(json.dumps({'name': name, 'security': security, 'passed': True}))\n"
	script += "    except Exception as exc:\n"
	script += "        print(json.dumps({'name': name, 'security': security, 'passed': False, 'detail': str(exc)}))\n"
	script += "    sys.stdout.flush()\n"
	return script
}

func quotePython(value string) string {
	escaped := ""
	for _, char := range value {
		switch char {
		case '\\':
			escaped += "\\\\"
		case '\'':
			escaped += "\\'"
		case '\n':
			escaped += "\\n"
		default:
			escaped += string(char)
		}
	}
	return "'" + escaped + "'"
}

func boolPython(value bool) string {
	if value {
		return "True"
	}
	return "False"
}
