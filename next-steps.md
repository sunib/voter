Let's call this a day: it's almost working and it's still a bit rough

These flows are never easy to get right

I do not want all these repetition: and I really do need a middleware to check for that rolling code -> on a high level it should be converted already. The LLMs find it pretty hard to do these things right.

What does make me happy:
* I managed to self create that secret
* I have the rights configured now at k8s side
* With a few little steps this is demo-able and can be a solid start of a conceptual talk

The interesting thing is now: How do I get my username?

One idea I had this morning:
* Put little envolopes on all chairs and map people to that -> Let's see if they find the audio levels acceptable.

Another 'crazy' idea is to turn off the lights with a CRD (and the technician as operator). Would that be to crazy?

For now let's be happy and close it of.


---

* The auth-service is now working in the cluster.
* You can also give a query param now: ?code=1234
* I added more tests and there is now a middleware to handle the token.
* I adjusted the RBAC
* I added build info on startup so that I can see quickly if my new code is running correctly on prod.
* You can now get the [current quizessions](https://present.z65.nl/apis/examples.configbutler.ai/v1alpha1/namespaces/present/quizsessions/kubecon-2026).
* The /session-info endpoints also works now
* Moved to /private and /public to make sure that we don't loose are tokens by accident.

Parked ideas:
* Having a audit webhook also would allow checking for unused rights in a Kubernetes cluster, so that you can apply least privelige in an easier way.
    * Is something avaialble already?
    * would it make a cool plugin later?
* Apply 'neat' to the output to only have the spec instead of the full k8s object -> would make it much more reable for the demo purposes that I have (the spec is still showing the truth) -> and it might come accross as very polutting if we don't do that...
    * That would require a 'real' proxy, since the current approach really passes directly into the Kubernetes API.

