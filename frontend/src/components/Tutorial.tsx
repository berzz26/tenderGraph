import { useEffect, useState } from "react";
import { ArrowRight, Check, X } from "lucide-react";

type Props = { onStart?: (bidID: string) => void };

const steps = [
  {
    title: "Start with a bidder",
    text: "Every investigation starts somewhere. Choose a bidder to see which tenders they participated in.",
  },
  {
    title: "Explore shared tenders (connections)",
    text: "These are tenders where this bidder appeared alongside other participants. Each connection is an observed procurement relationship.",
  },
  {
    title: "Discover recurring bidders (relationships)",
    text: "Some bidders appear alongside this bidder in multiple tenders. Look for repeated relationships, not just one-time overlaps.",
  },
  {
    title: "Expand the network",
    text: "Follow a connected bidder's relationships. Expanding preserves the original investigation context and reveals another level of the network.",
  },
  {
    title: "Inspect the evidence",
    text: "Every connection should have an explanation. Open a relationship to see which tender and source records support it.",
  },
  {
    title: "Identify a pattern",
    text: "You have followed one bidder through several tenders. Now consider what the overall pattern supports—and what remains uncertain.",
  },
];

export default function Tutorial({ onStart }: Props) {
  const [step, setStep] = useState<number | null>(null);
  useEffect(() => {
    if (localStorage.getItem("tg.tutorial.v1") !== "complete") setStep(0);
  }, []);
  useEffect(() => {
    const replay = () => {
      localStorage.removeItem("tg.tutorial.v1");
      setStep(0);
    };
    window.addEventListener("tg:tutorial:replay", replay);
    return () => window.removeEventListener("tg:tutorial:replay", replay);
  }, []);
  if (step === null) return null;
  const current = steps[step];
  const finish = () => {
    localStorage.setItem("tg.tutorial.v1", "complete");
    setStep(null);
  };
  const next = () => {
    if (step === 0) onStart?.("1760");
    if (step >= steps.length - 1) finish();
    else setStep(step + 1);
  };
  return (
    <div
      className="tutorial-layer"
      role="dialog"
      aria-label="TenderGraph guided investigation tutorial"
    >
      <div className="tutorial-scrim" />
      <div className="tutorial-card">
        <div className="tutorial-top">
          <span>
            GUIDED OVERVIEW · {step + 1} / {steps.length}
          </span>
          <button aria-label="Close tutorial" onClick={finish}>
            <X size={16} />
          </button>
        </div>
        <div className="tutorial-progress">
          <i style={{ width: ((step + 1) / steps.length) * 100 + "%" }} />
        </div>
        <h2>{current.title}</h2>
        <p>{current.text}</p>
        {step === 0 && (
          <p className="tutorial-detail">
            We will begin with Bid 1760, a real participant record in the
            development dataset. Its source name is Morrisons Lifecare Pvt Ltd.
          </p>
        )}
        {step > 0 && (
          <p className="tutorial-detail">
            This step explains the next part of a TenderGraph investigation. The
            guide does not change the page or select anything for you.
          </p>
        )}
        {step === steps.length - 1 && (
          <div className="tutorial-summary">
            <span>
              <Check size={14} /> Observed participation
            </span>
            <span>
              <Check size={14} /> Shared tender evidence
            </span>
            <span>
              <Check size={14} /> Uncertainty kept visible
            </span>
          </div>
        )}
        <div className="tutorial-actions">
          <button className="tutorial-skip" onClick={finish}>
            Skip
          </button>
          <button className="tutorial-next" onClick={next}>
            {step === steps.length - 1
              ? "Finish overview"
              : step === 0
                ? "Open bidder 1760"
                : "Next"}
            <ArrowRight size={15} />
          </button>
        </div>
      </div>
    </div>
  );
}
