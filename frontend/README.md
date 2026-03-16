# Vue 3 + TypeScript + Vite

This template should help get you started developing with Vue 3 and TypeScript in Vite. The template uses Vue 3 `<script setup>` SFCs, check out the [script setup docs](https://v3.vuejs.org/api/sfc-script-setup.html#sfc-script-setup) to learn more.

Learn more about the recommended Project Setup and IDE Support in the [Vue Docs TypeScript Guide](https://vuejs.org/guide/typescript/overview.html#project-setup).


To run a mock server:

```
npm run dev:mock --host
```


And for running with docker:

```bash
docker build -t present-yaml-frontend ./frontend
docker run --rm -p 8080:8080 present-yaml-frontend
```
docker build . -t zot.z65.nl/present/auth-service:v1
docker push zot.z65.nl/present/auth-service:v1
kubectl create secret docker-registry zot-pull   --docker-server=zot.z65.nl   --docker-username=admin   --docker-password='6LQnb6dUu18vhdEQfWRB' -n present

## Join flow

- Join with `X-Join-Code` only. The session name is resolved by auth-service.
- auth-service sets an encrypted session cookie that is reused for subsequent requests.
- The UI fetches `/session-info` after joining to display the session title/state.
