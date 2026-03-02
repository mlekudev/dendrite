# Dendrite: Fast Deterministic AI Text Content Detector

## What is Dendrite?

A text analysis tool that detects AI-generated content by measuring how coherent the text is at 8 levels of abstraction. Letters, words, sentences, paragraphs, sections, chapters, formalisms (or lack thereof) and ultimately piercing to the 8th layer, the identity of the author.

The difference between AI generated text is found somewhere in these 8 layers, and the current generative/derivative LLM systems lack the ability to connect from the first to the last without making errors somewhere in the middle, because of their inherently lossy representation and the compounding of these errors - meaning that as you go from left to right in that list, the probability of the model failing to recognise, either a false positive or false negative, is compounding exponentially.

This corresponds to a related domain that contains a similar problem, that being cryptography.

Cryptanalysis and security claims of a cryptographic primitive boil down to the robustness of the worst case scenario - for standard elliptic curves, for example, the "hard problem" is the discrete logarithm problem (DLP). 

The primary vulnerability this creates is related to quantum computers being able to execute the Shor algorithm in a short time, and the main threads of research for mitigation are based on Lattice, Hash and Code problems. 

Lattice is the most promising because it devolves to the shortest vector problem, which in layman's terms is that the more times you have to decide which direction at a fork in the road (the lattice node), the cost of computing the path explodes by the exponent of the number of branches.

Dendrite uses the Lattice strategy to uncover the structure of the text, from its most simple, to most abstract components, and then, from this path discovery, can provide metrics that match up with hidden signals that the LLM cannot conceal.

## Why this matters for AI safety

All current AI text detectors are built using the same flawed architecture as the thing they are trying to measure. 

The compounding is then compounded, meaning that defeating this detection through mapping the errors that cannot be eliminated from the system itself, is both an arms race, and a game of cat and mouse, or worse, of whack-a-mole. 

The adversary (mole) has a whole network of options it can use, and in addition to exploring those systematically, at some point, the player is going to forget they already saw a pattern before, because the chase led them to remove that detection method.

The compounding problem manifests differently across the domains, each of which this work addresses.

### Domains that this technology covers:

#### Academic integrity

The easy accessibility to advanced generative AI systems is a very big problem for academia. Students can easily cheat on exams, plagiarise while covering the fingerprint of the original text, and more alarmingly, contaminate the peer reviewed literature with such cleverly crafted deception that models, processes and conclusions can completely destroy the ability to evaluate whether a genuine discovery has been made, or if it is a cover for something more malign.

#### Social media content provenance

In social media, there is a tendency for the buildup of momentum of ideas, trends rise and fall and the natural trajectory of these seeks towards consensus and equilibrium. The generative text tools that are now widely available make it possible for an adversary to start with a synthesis, work back to an antithesis, and then find the thesis that enables the covert direction of the movement, and making it trend away from consensus to agreement with the threat actor's intentions. This applies equally to online rackets as it does to state level actors attacking other states' constituents by injecting poison into the dialog.

#### Model evaluation

Large language models must be evaluated before they can be released to the public for reasons of safety - if there are structural flaws in the neural network that open up exploitation by malicious actors, every method currently used to discover it is flawed, as explained in the foregoing. This also applies to in-house development, where instead of safety being the sole concern, the model must align with the agenda of the leadership of the organisation.

#### Synthetic data contamination

Prompt injection is a ticking timebomb that can be baked into the parameters of an LLM - it is only a matter of time - the depth at which the vulnerability exists works in the same way as the number of bits in a cryptographic algorithm is evaluated as an estimate of the cost of breaching security. This parallels the same process of probing LLMs to find where they can have their safety systems compromised, and enable usage outside of the intended safety boundaries of the developers.

## Current Results

Benchmarked against 500 human texts (Project Gutenberg), 24 Claude samples (3 tiers), and 1,100 samples from the RAID benchmark corpus (11 LLM models including GPT-4, ChatGPT, Llama, Mistral, Cohere, MPT):

- Human correct (true negative): 66.4%

- AI detected (true positive): 71.7%

- Balanced accuracy: 69.1%

Detection rates per model: ChatGPT 92%, MPT-chat 90%, GPT-4 89%, Llama-chat 84%, Mistral-chat 80%, Cohere 73%, Cohere-chat 70%, Mistral 63%, Claude Sonnet 62%, MPT 57%, GPT-3 50%, Claude Haiku 50%, GPT-2 47%, Claude Opus 25%.

The pattern is clear: chat-tuned models are easier to detect than base models. The guardrails create shallower patterns that are easier to detect than the raw corpus contains. These numbers from an early prototype trained on prose gathered from the Gutenberg Project texts. These are valid human inputs, but the differences between them and modern text content is substantial, and, ironically, also affected by the increasing presence of LLM generated content. The wetware is being trained by the hardware.

An improved training corpus with data harvested from the history of the Nostr protocol, where humans and machines have coexisted, along with a full range of text domains from LLM outputs, to academic and especially distributed systems discussions, to social exploitation activity, and consensus building in both protocol and ideology, provides a perfect cross-domain data set to test the discriminatory power of the algorithm.

### Evaluating this proposal with the prototype

In order to demonstrate directly how this works against the current prototype and its model built from the Gutenberg Project human content examples yielded results that are coherent with the claims that humans working closely with LLMs create a convergence that existing detection techniques would struggle to evaluate:

This proposal (excluding this subsection for reasons of recursion) was evaluated by the detector and classified at 29% AI confidence — a boundary case. The walk distance of 1.35 falls just inside the AI range (threshold 1.32), and the long-miss rate of 36.3% sits in the AI band (32–45% vs human 26–30%). Hit rates decline from 88% at the surface pass to 72% at the deepest.

A person (me) with a known measured top percentile pattern recognition ability, versus Claude Opus 4.6, the tool being used to develop the system, shows exactly what is predicted: the gap between the machine and the human converges in a way that reinforces the interpretation that existing methods cannot prevail.

## What this funding would support:

$25,600 over 6 months to bring Dendrite from research prototype to deployable tool across all four domains:

### Training corpus expansion and refinement

The initial test lattice is built from Project Gutenberg and needs a much larger body, and that body needs to contain a substantial amount of contemporary text, which initial data shows can be effectively extracted with moderate confidence of the boundary between generative and organic content.

### Adaptive scoring calibration

As the humans start to mimic the machines that are hiding in the text, the adversarial context is unstable. Like the game of whack-a-mole, the attacker can exploit vigilance fatigue and repeat near identical variants over a longer time frame, lowering their total investment. Humans and LLMs share in common the error rate problem, one from unstable analog signals, the other from rounding errors.

### Cross-domain evaluation

By using the results found at each of the 4 domains this work targets, the overlaps between the structure of each feeds back information that informs the others. Reinforcement at these boundaries allows the lattice to be a single kernel, and enables potential future application to other domains that have overlaps with what is already established, kickstarting the development process.

### Public benchmark infrastructure

All current benchmarks for LLM evaluation fail in one way or another, and adjusting them can lead to weakening one part by strengthening another. Dendrite would allow one to hold the parts that work, while adjusting the parts that don't, without contaminating the process in ways that defeats the purpose - objective measurements that back up claims made by the marketing of these systems to the public, business and government.

## Failure modes

The detector may not work well across domains. Academic prose and social media posts have different formal structure. The calibration may reveal that there is more isolation than anticipated between the domains. 

The human detection rate (66.4%) needs improvement, to become more precise versus the machine detection rate at 71.4%, a margin of 5% - sufficient to indicate potential, but likely indicative of a much more expansive corpus and cycles of revision than the initial tests.

Future LLM architectures might include mechanisms that improve global structural coherence. Humans influenced by this may also then become on average closer to the models. The detection system needs to be able to overcome this convergence by always landing right on that margin and cutting the two sides cleanly and unambiguously.

These risks are manageable within the proposed scope and budget, and the evaluation work is specifically designed to quantify them.

## Budget

$25,600 for 6 months of full-time work by an independent developer in southeastern Europe:

- $21,400 for researcher time: 3,567/month

- $3,400 for development workstation - Framework Desktop, Ryzen AI Max 395+ with 128gb of unified memory. Anything smaller is not credible for offline testing, a sufficiently large LLM must be able to run in parallel with the detector, and this eliminates ongoing costs to cover cloud model access.

- $800 Corpus acquisition and extended training compute (mainly electricity and internet connectivity).

No recurring compute costs. No API costs. No team costs. Single-person project, commodity hardware, resilient and able to be executed in the kind of quiet environment that is conducive to recognising patterns before they become noise.

## Deliverables

- Published benchmarks against RAID, MAGE, and live LLM APIs across 4 application domains

- Calibrated scoring with published accuracy metrics per domain

- Detection scores for 15+ LLM models

- Technical paper documenting the method, evaluation results, and limitations

- All code open source, published on github

## About me

Independent researcher, self-taught from first principles - learned english through phonetics at age 4, wrote GUI code from concepts gleaned from computer literature at age 9, and never stopped building and exploring from there.

The trajectory moved through systems programming, distributed protocols, cryptography and coding theory before arriving at machine intelligence.

The path was not academic - it ran through environments where pattern recognition was not theoretical, but a matter of survival - contexts ranging from social systems under institutional decay, to subcultures built on self-reliance and improvisation, to protocol ecosystems where the line between community and capture is drawn by those who fund the development of its infrastructure. Each of these demanded the ability to distinguish coherent structure from the projection of coherence - to recognise the difference between something that works, and something that works well enough to pass casual inspection.

This is the same discrimination that Dendrite makes - the system was not designed in isolation from the problem it solves. It emerged from sustained, direct engagement with the full spectrum of contexts where growth and decay coexist in a balance where decay was winning.

With this grant in hand, I can build the tool that I am quite uniquely equipped to build, and motivated to build, by the experience that led to the idea.