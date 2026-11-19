"""Draft measurement only. No Gent codec implementation or format change."""
import collections, hashlib, json, pathlib, sys

class Number(str):
    pass

def emit(value):
    if isinstance(value, Number):
        return str(value)
    if isinstance(value, dict):
        return '{' + ','.join(emit(k) + ':' + emit(v) for k,v in value.items()) + '}'
    if isinstance(value, list):
        return '[' + ','.join(map(emit, value)) + ']'
    return json.dumps(value, ensure_ascii=False, separators=(',', ':')).replace('&', '\\u0026')\
        .replace('<', '\\u003c').replace('>', '\\u003e')\
        .replace('\u2028', '\\u2028').replace('\u2029', '\\u2029')

def parse(text):
    return json.loads(text, parse_int=Number, parse_float=Number)

# These names select dictionary candidates, not Go types. The generic record grammar preserves
# every original key and value even when a custom payload happens to use these same names.
metadata = {'type', 'declaredType', 'element', 'key'}
def measure(path):
    original = path.read_text()
    root = parse(original)
    assert emit(root) == original, 'Different JSON spelling; do not claim byte equality.'
    names = sorted({v for k,v in pairs(root) if k in metadata and type(v) is str})
    name_ids = {v:i for i,v in enumerate(names)}
    # Stage 1 uses explicit typed markers. All original objects/arrays use a generic grammar,
    # so an application field named $type or $ref cannot be confused with a format instruction.
    # Its compact JSON size is measured separately as an illustrative metadata-only encoding.
    def intern(x):
        if isinstance(x, list): return [intern(v) for v in x]
        if not isinstance(x, dict): return x
        return {k: ({'$type':name_ids[v]} if type(v) is str else {'$value':intern(v)})
                if k in metadata else intern(v) for k,v in x.items()}
    def expand_names(x):
        if isinstance(x,list): return [expand_names(v) for v in x]
        if not isinstance(x,dict): return x
        return {k: (names[int(v['$type'])] if '$type' in v else expand_names(v['$value']))
                if k in metadata else expand_names(v) for k,v in x.items()}
    assert emit(expand_names(intern(root))) == original
    first = {'format':'gent-table-draft-1','types':names,'root':intern(root)}

    layouts, layout_ids = [], {}
    def pack(x):
        if isinstance(x, list): return ['array',*[pack(v) for v in x]]
        if not isinstance(x, dict): return x
        layout, values = [], []
        for key,value in x.items():
            if key in metadata and type(value) is str:
                layout.append([key, {'type':name_ids[value]}])
            elif key == 'version' and isinstance(value, Number):
                layout.append([key, {'constant':value}])
            else:
                layout.append([key]); values.append(pack(value))
        signature = emit(layout)
        if signature not in layout_ids:
            layout_ids[signature] = len(layouts); layouts.append(layout)
        return [layout_ids[signature], *values]
    packed = pack(root)
    second = {'format':'gent-table-draft-2','types':names,'layouts':layouts,'root':packed}
    def unpack(x, shared=()):
        if not isinstance(x, list): return x
        if x[0] == 'array': return [unpack(v,shared) for v in x[1:]]
        if x[0] == 'ref': return unpack(shared[int(x[1])],shared)
        values = iter(x[1:]); result = {}
        for field in layouts[int(x[0])]:
            if len(field) == 1: result[field[0]] = unpack(next(values),shared)
            elif 'type' in field[1]: result[field[0]] = names[int(field[1]['type'])]
            else: result[field[0]] = field[1]['constant']
        assert next(values, None) is None
        return result
    assert emit(unpack(packed)) == original

    # Share the largest remaining exact repeats first. Each reference expands independently;
    # wire deduplication does not imply new mutable Go aliasing. No reference crosses a file.
    shared, reduced = [], packed
    while True:
        counts, values = collections.Counter(), {}
        def visit(x):
            if not isinstance(x, list) or x[0] == 'ref': return
            key=emit(x); counts[key]+=1; values[key]=x
            for child in x[1:]: visit(child)
        visit(reduced)
        reference = ['ref',len(shared)]
        candidates = [( (count-1)*len(key.encode())-count*len(emit(reference))-1, key)
                      for key,count in counts.items() if count>1]
        if not candidates: break
        gain,key = max(candidates)
        if gain<=0: break
        saved=values[key]
        def replace(x):
            if not isinstance(x,list) or x[0]=='ref': return x
            if emit(x)==key: return reference
            return [x[0], *[replace(v) for v in x[1:]]]
        reduced=replace(reduced); shared.append(saved)
    third = {'format':'gent-table-draft-3','types':names,'layouts':layouts,
             'shared':shared,'root':reduced}
    # Reload the emitted document: the tables in that file must be sufficient by themselves.
    reloaded = parse(emit(third))
    names, layouts = reloaded['types'], reloaded['layouts']
    expanded=emit(unpack(reloaded['root'], reloaded['shared']))
    assert expanded==original
    for number,data in [(1,first),(2,second),(3,third)]:
        path.with_suffix(f'.stage{number}.json').write_text(emit(data))
    path.with_suffix('.expanded.json').write_text(expanded)
    result={'file':path.name,'original':len(original.encode()),
            'type_table':len(emit(first).encode()),'layouts':len(emit(second).encode()),
            'shared_values':len(emit(third).encode()),'types':len(names),
            'layout_count':len(layouts),'shared_count':len(shared),
            'exact_expansion':True,'sha256':hashlib.sha256(original.encode()).hexdigest()}
    print(json.dumps(result)); return result

def pairs(x):
    if isinstance(x,dict):
        for k,v in x.items(): yield k,v; yield from pairs(v)
    elif isinstance(x,list):
        for v in x: yield from pairs(v)

if __name__ == '__main__':
    for name in sys.argv[1:]:
        measure(pathlib.Path(name))
