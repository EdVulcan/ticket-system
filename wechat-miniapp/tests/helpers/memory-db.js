'use strict';
// Doc-only transaction double with snapshot reads, staged writes and MVCC conflicts.
// Deliberately implements only the doc subset used by our core. It does not model range locks.
const clone = (value) => value === undefined ? undefined : structuredClone(value);
function memoryDb(seed = {}) {
  const values = new Map();
  const versions = new Map();
  for (const [collection, documents] of Object.entries(seed)) for (const document of documents) values.set(`${collection}/${document._id}`, clone(document));
  const getPath = (value, path) => path.split('.').reduce((current, key) => current && current[key], value);
  function patch(value, change) {
    const next = clone(value);
    for (const [path, item] of Object.entries(change)) {
      const keys = path.split('.');
      const last = keys.pop();
      let target = next;
      for (const key of keys) { target[key] = target[key] || {}; target = target[key]; }
      if (item && item.__command === 'inc') target[last] = (target[last] || 0) + item.value;
      else if (item && item.__command === 'remove') delete target[last];
      else target[last] = clone(item);
    }
    return next;
  }
  function reference(collection, id, tx) {
    const key = `${collection}/${id}`;
    const read = () => {
      if (!tx) return clone(values.get(key));
      tx.reads.add(key);
      return clone(tx.writes.has(key) ? tx.writes.get(key) : tx.snapshot.get(key));
    };
    const write = (value) => {
      if (tx) { tx.reads.add(key); tx.writes.set(key, clone(value)); }
      else { values.set(key, clone(value)); versions.set(key, (versions.get(key) || 0) + 1); }
    };
    return {
      async get() { return { data: read() || null }; },
      async set({ data }) { write(Object.assign({}, data, { _id: id })); },
      async update({ data }) { const value = read(); if (!value) throw new Error('DOCUMENT_NOT_FOUND'); write(patch(value, data)); },
      async remove() { write(undefined); }
    };
  }
  const db = {
    command: { inc: (value) => ({ __command: 'inc', value }), remove: () => ({ __command: 'remove' }), lt: (value) => ({ __command: 'lt', value }), in: (value) => ({ __command: 'in', value }) },
    serverDate: () => new Date(),
    collection(name) {
      return {
        doc: (id) => reference(name, id),
        where(condition) {
          let limit = Infinity;
          const sortFields = [];
          const query = {
            limit(value) { limit = value; return query; },
            orderBy(path, direction) { sortFields.push([path, direction]); return query; },
            async get() {
              const rows = [...values.entries()].filter(([key, value]) => key.startsWith(`${name}/`) && value && Object.entries(condition).every(([path, expected]) => expected && expected.__command === 'lt' ? getPath(value, path) < expected.value : expected && expected.__command === 'in' ? expected.value.includes(getPath(value, path)) : getPath(value, path) === expected)).map(([, value]) => value);
              rows.sort((a, b) => { for (const [path, direction] of sortFields) { const av = getPath(a, path); const bv = getPath(b, path); const compared = av < bv ? -1 : av > bv ? 1 : 0; if (compared) return direction === 'desc' ? -compared : compared; } return 0; });
              return { data: rows.slice(0, limit).map(clone) };
            }
          };
          return query;
        }
      };
    },
    async startTransaction() {
      const tx = { snapshot: new Map([...values].map(([key, value]) => [key, clone(value)])), versions: new Map(versions), reads: new Set(), writes: new Map() };
      return {
        collection: (name) => ({ doc: (id) => reference(name, id, tx) }),
        async commit() {
          for (const key of tx.reads) if ((versions.get(key) || 0) !== (tx.versions.get(key) || 0)) throw new Error('TRANSACTION_CONFLICT');
          for (const [key, value] of tx.writes) { values.set(key, clone(value)); versions.set(key, (versions.get(key) || 0) + 1); }
        },
        async rollback() { tx.writes.clear(); }
      };
    },
    read: (collection, id) => clone(values.get(`${collection}/${id}`)),
    all: (collection) => [...values.entries()].filter(([key, value]) => key.startsWith(`${collection}/`) && value).map(([, value]) => clone(value))
  };
  return db;
}
module.exports = { memoryDb };
