I would like this repo to contain a web application that serves a frontend that is usable on a phone.

I'm allowed to give a [Kubecon talk](https://colocatedeventseu2026.sched.com/?_gl=1*1651shg*_gcl_au*MTcwNjUwOTkzLjE3Njc5NjQyNTY.*FPAU*MTcwNjUwOTkzLjE3Njc5NjQyNTY.) in march this year. I've added the [talk outline](talk-outline.md)

It is to be used during presentations as a questionaire tool for the room. It's going to be a 'special' tool since it will be using a public Kubernetes API server as it's API. We won't save things in a regular database, just store it as a CRD in the Kubernetes API server.

The only authentication that we will do is a secret code in the URL: which is provided through a QR code at thes start of the session.

There is also no need to obtain any personal details of attendees, except for what they enter themselves.

The goal is to showcase that everybody can create valid yaml files: reversing gitops will allow them to.

To start with I would like to create an example questionaire to 'read' the temperature in the room: I would like to set the tone and to show them without telling why/how.

Let's have a few example questions to start with:

* How did you liked the lunch?
* How many km's did you traveled to get here? (approxomately).
* How do you self service your users?
    * We allow them to manage their own part of some Kubernetes cluster
    * We crafted our own self-service application
    * A form of ticketing system
* How do you like the presentation so far? Scale 0-10
* Did you ever considered to craft your own CRD?
* Do you wrestle with getting your Abstraction right?

---

## Project direction (chosen path)

This repo is a demo companion for the talk in [`talk-outline.md`](talk-outline.md:1).

Main architectural choices (kept intentionally simple to match the talk narrative):

- Kubernetes API server is fully exposed to the internet, protected by Traefik.
- A small forward-auth service validates a join code and sets up a per-browser device session.
- Real Kubernetes tokens never leave the server side (Traefik injects `Authorization` upstream).
- Frontend is a static SPA: Vue 3 + Vite + TypeScript + shadcn-vue.

Primary docs:

- [`ARCHITECTURE.md`](ARCHITECTURE.md:1)
- [`FRONTEND.md`](FRONTEND.md:1)

Alternatives and trade-offs:

- [`docs/alternatives/README.md`](docs/alternatives/README.md:1)

# Skills

Started to get some understanding for skills: I just installed
https://skills.sh/giuseppe-trisciuoglio/developer-kit/shadcn-ui
