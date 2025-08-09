Kernel Computer Operator API. To use on PORT=9999

---

# Build
Using bun builder
```bash
bun build:linux # bin : dist/kernel-operator-api
```

---

# Tests

```bash
bun test.js browser --watch
bun test.js bus --watch
bun test.js clipboard --watch
bun test.js fs --watch
bun test.js fs-nodelete --watch
bun test.js health --watch
bun test.js input --watch
bun test.js logs --watch
bun test.js macros --watch
bun test.js metrics --watch
bun test.js network --watch
bun test.js os --watch
bun test.js pipe --watch
bun test.js process --watch
bun test.js recording --watch
bun test.js recording-nodelete --watch
bun test.js screenshot --watch
bun test.js scripts --watch
bun test.js scripts-nodelete --watch
bun test.js stream --watch
```

---

# Notes

#### Add to/ensure exists in Dockerfile

```bash
wayland tools , wl-paste , xclip # should be part of wayland but missing in my dev vm
```